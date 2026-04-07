package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Client struct {
	host   string
	model  string
	sem    chan struct{} // capacity 1: serializes concurrent requests
	logMu  sync.Mutex
	logger io.Writer // optional — logs every request/response when set
}

// SetLogger enables request/response logging to w (e.g. an open *os.File).
// Pass nil to disable.
func (c *Client) SetLogger(w io.Writer) {
	c.logMu.Lock()
	c.logger = w
	c.logMu.Unlock()
}

func New() *Client {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	} else if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	model := os.Getenv("UNROT_MODEL")
	if model == "" {
		model = "qwen2.5:7b"
	}
	return &Client{host: host, model: model, sem: make(chan struct{}, 1)}
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Message message `json:"message"`
}

type QuestionType int

const (
	TypeFlashcard QuestionType = iota
	TypeExplain
	TypeFillBlank
	TypeFinishCode
	TypeMultiChoice
	TypeCompare
	TypeScenario
	TypeOrdering
	TypeCodeOutput
	TypeDebug
	TypeDecision
	TypeArchitecture
	TypeRefactor
)

func (t QuestionType) String() string {
	switch t {
	case TypeExplain:
		return "explain"
	case TypeFillBlank:
		return "fill-blank"
	case TypeFinishCode:
		return "finish-code"
	case TypeMultiChoice:
		return "multiple-choice"
	case TypeCompare:
		return "compare"
	case TypeScenario:
		return "scenario"
	case TypeOrdering:
		return "ordering"
	case TypeCodeOutput:
		return "code-output"
	case TypeDebug:
		return "debug"
	case TypeDecision:
		return "decision"
	case TypeArchitecture:
		return "architecture"
	case TypeRefactor:
		return "refactor"
	default:
		return "flashcard"
	}
}

// UsesTypedAnswer returns true if this question type supports typed answers.
func (t QuestionType) UsesTypedAnswer() bool {
	return t != TypeMultiChoice && t != TypeOrdering
}

type Difficulty int

const (
	DiffBasic        Difficulty = iota // new or weak (strength 0-0.3)
	DiffIntermediate                   // learning (strength 0.3-0.7)
	DiffAdvanced                       // strong (strength 0.7+)
)

func (d Difficulty) String() string {
	switch d {
	case DiffIntermediate:
		return "intermediate"
	case DiffAdvanced:
		return "advanced"
	default:
		return "basic"
	}
}

// DifficultyFromConfidence maps a confidence level (0-5) to a difficulty level.
func DifficultyFromConfidence(c int) Difficulty {
	switch {
	case c >= 4:
		return DiffAdvanced
	case c >= 3:
		return DiffIntermediate
	default:
		return DiffBasic
	}
}

type Question struct {
	Type        QuestionType
	Difficulty  Difficulty
	Text        string
	Answer      string
	Explanation string   // why the answer is correct — shown after grading
	Options     []string // for multiple choice
	CorrectIdx  int      // for multiple choice
}

// Message is an exported chat message for conversation tracking.
type Message struct {
	Role    string
	Content string
}

// AllTypes returns the full set of question types for random selection.
var AllTypes = []QuestionType{TypeFlashcard, TypeExplain, TypeFillBlank, TypeFinishCode, TypeMultiChoice, TypeCompare, TypeScenario, TypeOrdering, TypeCodeOutput, TypeDebug, TypeDecision, TypeArchitecture, TypeRefactor}

// Challenge represents a standalone coding challenge (not tied to a knowledge file).
type Challenge struct {
	Title       string
	Description string
	Concept     string // brief knowledge section shown below the problem
	Language    string
	Difficulty  Difficulty
}

// ChallengeGrade is ollama's evaluation of a challenge submission.
type ChallengeGrade struct {
	Score      int // 0-100
	Correct    bool
	Efficiency string // "optimal", "acceptable", "suboptimal"
	Feedback   string
}

// GenerateQuestion generates a question of the given type. If qtype is -1, picks randomly.
func (c *Client) GenerateQuestion(ctx context.Context, content, filename string, qtype QuestionType, diff Difficulty) (*Question, error) {
	if qtype < 0 {
		qtype = AllTypes[rand.Intn(len(AllTypes))]
	}

	system := promptFor(qtype, diff)
	user := fmt.Sprintf("Document: %s\n\n%s", filename, content)

	resp, err := c.Chat(ctx, system, user)
	if err != nil {
		return nil, err
	}

	q, err := parseResponse(resp, qtype)
	if err != nil {
		return nil, err
	}
	q.Difficulty = diff
	return q, nil
}

// GenerateKnowledge creates a knowledge document about a topic.
// chatHistory contains the clarification conversation for context.
// If existingFiles is non-nil, ollama suggests placement (domain/slug) based on existing structure.
// Returns content, suggested domain, suggested slug.
func (c *Client) GenerateKnowledge(ctx context.Context, topic string, chatHistory []Message, existingFiles []string) (content, domain, slug string, err error) {
	var placementInstr string
	if len(existingFiles) > 0 {
		var fileList strings.Builder
		for _, f := range existingFiles {
			fileList.WriteString("- " + f + "\n")
		}
		placementInstr = fmt.Sprintf(`
On the FIRST line of your response, output EXACTLY:
PLACE: domain/slug
where domain is the best-fitting folder name and slug is a lowercase-hyphenated filename (no extension).
Use an existing domain if appropriate, or suggest a new one.
Then a blank line, then the knowledge document.

Existing knowledge structure:
%s`, fileList.String())
	}

	system := fmt.Sprintf(`You are a knowledge base writer creating study material for a software developer.
The preceding conversation shows the user clarifying what they want to learn — their level, focus areas, and what confuses them. Use ALL of that context to tailor the document.
%s
Rules:
- Start with: # Topic Name
- Then a 1-2 sentence definition that captures the core idea
- Use these sections (include all that apply):

## Key Facts
- Bullet points of core concepts, definitions, important numbers
- Each bullet should be a specific, testable fact — not vague summaries
- Include concrete numbers, thresholds, defaults where relevant

## How It Works
- Explain the mechanism/process step by step
- Include practical code examples in fenced code blocks
- Show input → output or before → after where helpful

## Gotchas
- Common mistakes, surprising behavior, edge cases
- "What most people get wrong about this"
- Include the WHY — not just "don't do X" but "X fails because..."

## Connections
- requires: domain/slug (list hard prerequisites the reader should know first, e.g. "- requires: go/goroutines")
- How this relates to other concepts the developer likely knows
- When to use this vs alternatives, with concrete decision criteria

- NO frontmatter, NO markdown code fences wrapping the whole document
- Keep it 30-60 lines — dense and useful, not verbose, but don't cut important details
- Focus on "things you need to remember" and "things that would trip you up"
- Every bullet should be something worth quizzing on
- Use bullet points and code blocks liberally
- Tailor depth to what the conversation revealed about the user's level`, placementInstr)

	// Build messages: the clarification conversation + final generation request
	msgs := make([]Message, 0, len(chatHistory)+1)
	msgs = append(msgs, chatHistory...)
	msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf("Now generate the knowledge document for: %s", topic)})

	resp, err2 := c.ChatWithHistory(ctx, system, msgs)
	if err2 != nil {
		return "", "", "", err2
	}

	resp = strings.TrimSpace(resp)

	// Parse PLACE: line if present
	if strings.HasPrefix(resp, "PLACE:") {
		lines := strings.SplitN(resp, "\n", 2)
		place := strings.TrimSpace(strings.TrimPrefix(lines[0], "PLACE:"))
		parts := strings.SplitN(place, "/", 2)
		if len(parts) == 2 {
			domain = strings.TrimSpace(parts[0])
			slug = strings.TrimSpace(parts[1])
		}
		if len(lines) > 1 {
			resp = strings.TrimSpace(lines[1])
		}
	}

	return resp, domain, slug, nil
}

// UpdateKnowledge takes the clarification chat history, existing file content,
// and returns a merged/updated knowledge document.
func (c *Client) UpdateKnowledge(ctx context.Context, topic string, chatHistory []Message, existingContent string) (string, error) {
	system := `You are a knowledge base writer updating an existing study document for a software developer.
The preceding conversation shows what the user wants to add or change. Use that context to understand their intent.

Rules:
- Preserve the existing structure (# title, ## sections)
- Merge new information into the appropriate existing sections
- Add new sections only if the information doesn't fit existing ones
- Add new bullet points, code examples, gotchas based on what the conversation revealed
- Each bullet should be a specific, testable fact — not vague summaries
- Do NOT duplicate information already in the document
- Do NOT remove existing content unless the user explicitly asked to correct something
- Keep it 30-70 lines total — dense and useful
- Output ONLY the updated markdown document, no commentary or explanation
- Preserve any ## Notes section at the end unchanged
- NO markdown code fences wrapping the whole document`

	// Build messages: the clarification conversation + the existing doc to update
	msgs := make([]Message, 0, len(chatHistory)+1)
	msgs = append(msgs, chatHistory...)
	msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf("Now update this existing document with what we discussed.\n\nExisting document:\n%s", existingContent)})

	return c.ChatWithHistory(ctx, system, msgs)
}

func difficultyClause(d Difficulty) string {
	shared := `
CRITICAL — ANSWER PROTECTION:
- NEVER include the answer, or a close paraphrase of it, anywhere in the question text
- NEVER use phrasing like "What is X?" when X is literally stated as a definition in the document — rephrase to test understanding from a different angle
- If the question mentions a concept, do NOT also mention the answer concept in the same sentence
- The user will try again if they get it wrong, so making it guessable defeats the purpose

VARIETY & QUALITY:
- Pick a RANDOM section/bullet from the document — do NOT always target the first or most obvious fact
- Rephrase using your own words — never copy document sentences into the question
- The question should require THINKING, not pattern-matching against the text
- Wrong options (if MC) must be plausible — no obviously wrong filler
- Prefer questions that test "would you recognize this in practice" over "can you recite the definition"`

	switch d {
	case DiffAdvanced:
		return `Difficulty: ADVANCED
- Ask about edge cases, subtle gotchas, "what happens when...", or tricky interactions
- Assume the user knows the basics — test if they REALLY understand
- Target things that would trip someone up in a real project or interview` + shared
	case DiffIntermediate:
		return `Difficulty: INTERMEDIATE
- Ask about practical application, trade-offs, or "why would you choose X over Y"
- Go beyond definitions — test if they can apply the knowledge
- Focus on decisions a developer makes when actually using this` + shared
	default:
		return `Difficulty: BASIC
- Ask about core concepts, key definitions, or fundamental "what does X do"
- Keep in mind the user is a beginner, so dont ask things that are too advanced for them.
- Keep it approachable — one clear concept per question
- The answer should be something concrete, not a vague explanation` + shared
	}
}

func explanationClause() string {
	return `E: <explanation — TEACH the concept in 2-4 sentences. Include a concrete example (code, input→output, or before→after). Explain WHY, not just WHAT. Must be factually correct. Can span multiple lines. DO NOT GIVE AWAY THE ANSWER IN THE EXPLANATION.>`
}

func promptFor(t QuestionType, diff Difficulty) string {
	dc := difficultyClause(diff)

	ec := explanationClause()

	switch t {
	case TypeExplain:
		return fmt.Sprintf(`Generate ONE quiz question from the document below.
%s

Ask WHY or HOW something works, not just WHAT it is. Focus on mechanisms, processes, code examples, and how concepts relate to each other.

Output EXACTLY this format (3 lines, no other text):
Q: <question>
A: <answer in 1-2 sentences>
%s

Example:
Q: What is the M:N scheduling model in Go?
A: M goroutines are multiplexed onto N OS threads. The Go runtime scheduler assigns goroutines to threads, so you can run thousands of goroutines on just a few threads.
E: Think of it like a restaurant — M orders (goroutines) handled by N cooks (OS threads). You don't need one cook per order. The scheduler decides which cook handles which order. This is why you can have 100k goroutines on 8 threads.`, dc, ec)

	case TypeFillBlank:
		return fmt.Sprintf(`Generate ONE fill-in-the-blank question from the document below.
%s

Find a key fact, definition, or important number in the document and blank out the critical technical term.

Rules:
- Replace ONE key term with ___
- The answer must be a specific word or short phrase (1-4 words)
- Do NOT blank out vague words — blank out the important technical term

Output EXACTLY this format (3 lines, no other text):
Q: <sentence with ___ blank>
A: <missing word or short phrase>
%s

Example:
Q: Goroutines yield at function calls, channel ops, and ___ — they are not preemptive.
A: runtime checkpoints
E: Go's scheduler is cooperative — goroutines don't get interrupted mid-execution. They must voluntarily yield by hitting a checkpoint (like a function call or channel op). This means a tight CPU loop with no function calls can starve other goroutines.`, dc, ec)

	case TypeFinishCode:
		return fmt.Sprintf(`Generate ONE code question from the document below.
%s

Show a short code snippet with one MISSING line replaced by the comment // ???
The answer is the missing line of code.
Do NOT use backticks or markdown. Write code as plain text.

Output EXACTLY this format, no other text:
Q: <line 1>
<line 2>
// ???
<line 4>
A: <the missing line of code>
%s

Example:
Q: var wg sync.WaitGroup
wg.Add(1)
go func() {
// ???
fmt.Println("done")
}()
wg.Wait()
A: defer wg.Done()
E: wg.Add(1) increments a counter. wg.Done() decrements it. wg.Wait() blocks until the counter hits 0. If you forget wg.Done(), Wait() blocks forever — your program hangs. Using defer ensures Done() runs even if the goroutine panics.`, dc, ec)

	case TypeMultiChoice:
		return fmt.Sprintf(`Generate ONE multiple choice question from the document below.
%s

Focus on gotchas, surprising behaviors, common mistakes, or edge cases — test misconceptions.

Rules:
- 4 options labeled A) B) C) D), exactly one correct
- Wrong options should be plausible but clearly wrong to someone who knows the material
- Do NOT make the correct answer the longest or most detailed option

Output EXACTLY this format (no other text):
Q: <question>
A) <option>
B) <option>
C) <option>
D) <option>
ANSWER: <letter>
%s

Example:
Q: What is the cost of creating a new goroutine?
A) ~1MB of stack space
B) ~2KB of stack space
C) ~64KB of stack space
D) ~8KB of stack space
ANSWER: B
E: Goroutines start with a tiny ~2KB stack that grows as needed. OS threads start at ~1MB fixed. That 500x difference is why Go can run millions of goroutines — each one barely costs anything until it actually needs more stack space.`, dc, ec)

	case TypeCompare:
		return fmt.Sprintf(`Generate ONE compare/contrast question from the document below.
%s

Ask the user to compare two related concepts, tools, approaches, or techniques mentioned in the document. Focus on practical differences — when to use one vs the other, trade-offs, or how they behave differently.

Rules:
- Both things being compared MUST appear in the document
- Ask about meaningful differences, not surface-level trivia
- The answer should highlight 2-3 key differences

Output EXACTLY this format (3 lines, no other text):
Q: <question comparing two things>
A: <2-3 key differences in 2-4 sentences>
%s

Example:
Q: How do mutexes and channels differ for sharing state between goroutines?
A: Mutexes protect shared memory with lock/unlock — simple for single values but error-prone (deadlocks). Channels pass ownership of data between goroutines — safer by design, but add overhead and can deadlock too if misused. Rule of thumb: use channels for coordination, mutexes for simple shared counters.
E: Think of mutexes as a bathroom lock — one person at a time, everyone waits. Channels are like passing a note — the data moves to whoever needs it. Go's motto "share memory by communicating" favors channels, but mutexes are fine for simple cases like a hit counter.`, dc, ec)

	case TypeScenario:
		return fmt.Sprintf(`Generate ONE scenario-based question from the document below.
%s

Describe a realistic situation or problem and ask what would happen, what the user should do, or what the outcome would be. Test applied knowledge — can they use what they learned in a real context?

Rules:
- The scenario must be grounded in concepts from the document
- Ask "what happens when...", "how would you handle...", or "what goes wrong if..."
- The answer should demonstrate applied understanding, not just recall

Output EXACTLY this format (3 lines, no other text):
Q: <scenario description + question>
A: <what happens or what to do, 1-3 sentences>
%s

Example:
Q: You deploy a Go service that spawns a goroutine per request but never cancels them when clients disconnect. Traffic spikes to 10x normal. What happens?
A: Goroutines accumulate because disconnected clients' goroutines keep running. Memory grows until the service OOMs or hits resource limits. Fix: use context.Context from the request and select on ctx.Done() to cancel work when clients disconnect.
E: This is a goroutine leak — one of the most common Go production issues. Each goroutine is cheap (~2KB) but 10x traffic with no cleanup means they pile up. The fix is always context propagation: pass the request context down and check for cancellation.`, dc, ec)

	case TypeOrdering:
		return fmt.Sprintf(`Generate ONE ordering/sequence question from the document below.
%s

Present a process, workflow, or sequence of steps from the document and ask the user to identify the correct order. Use multiple choice where each option is a different ordering.

Rules:
- The steps must come from the document
- Use 3-5 distinct steps that have a clear correct order
- Each option should be a plausible but different ordering
- Label the steps with short names (2-5 words each)

Output EXACTLY this format (no other text):
Q: What is the correct order of these steps? <context>
A) <step> → <step> → <step> → <step>
B) <step> → <step> → <step> → <step>
C) <step> → <step> → <step> → <step>
D) <step> → <step> → <step> → <step>
ANSWER: <letter>
%s

Example:
Q: What is the correct order for setting up a Go module from scratch?
A) go mod init → write code → go mod tidy → go build
B) write code → go mod init → go build → go mod tidy
C) go mod init → go build → write code → go mod tidy
D) go build → go mod init → write code → go mod tidy
ANSWER: A
E: go mod init creates the go.mod file (names your module). Then you write code with imports. go mod tidy resolves and downloads dependencies. Finally go build compiles. You can't tidy before writing code (nothing to resolve) and you can't build without a module.`, dc, ec)

	case TypeCodeOutput:
		return fmt.Sprintf(`Generate ONE "what does this code output?" question from the document below.
%s

Show a short code snippet (3-8 lines) based on concepts in the document and ask what it outputs or what value a variable holds. Test whether the user can mentally trace execution.

Rules:
- Code must relate to concepts in the document
- Keep it short — max 8 lines of code
- The output should be specific and deterministic (not "it depends")
- Do NOT use backticks or markdown. Write code as plain text.

Output EXACTLY this format (no other text):
Q: What does this code output?
<code lines>
A: <the exact output or value>
%s

Example:
Q: What does this code output?
ch := make(chan int, 2)
ch <- 1
ch <- 2
fmt.Println(<-ch)
fmt.Println(<-ch)
A: 1 then 2 (each on a new line)
E: Buffered channels are FIFO — first in, first out. ch <- 1 goes in first, so <-ch pulls it out first. The buffer size of 2 means both sends succeed without blocking. If the buffer were 0 (unbuffered), the sends would block without a receiver on another goroutine.`, dc, ec)

	case TypeDebug:
		return fmt.Sprintf(`Generate ONE "find the bug" question from the document below.
%s

Show a short code snippet (3-8 lines) that contains ONE subtle bug related to concepts in the document. Ask the user to identify the bug and explain the fix.

Rules:
- The bug must relate to a concept covered in the document
- Keep the code short — max 8 lines
- The bug should be subtle but real (not a syntax error) — race conditions, off-by-one, missing cleanup, wrong API usage
- Do NOT use backticks or markdown. Write code as plain text.

Output EXACTLY this format (no other text):
Q: What is the bug in this code?
<code lines>
A: <the bug and how to fix it, 1-2 sentences>
%s

Example:
Q: What is the bug in this code?
var mu sync.Mutex
func increment(counter *int) {
    mu.Lock()
    *counter++
    if *counter > 100 {
        return
    }
    mu.Unlock()
}
A: The early return skips mu.Unlock(), causing a permanent deadlock. Fix: use defer mu.Unlock() right after Lock().
E: This is the classic "forgot to unlock" bug. When counter > 100, the function returns but the mutex stays locked — every future call to increment blocks forever. defer mu.Unlock() guarantees the unlock runs no matter how the function exits (return, panic, etc.).`, dc, ec)

	case TypeDecision:
		return fmt.Sprintf(`Generate ONE design decision question from the document below.
%s

Ask WHY a particular design choice was made — why this approach over alternatives. Focus on trade-offs, constraints, and reasoning. Great for interview prep: "Tell me why you chose X."

Output EXACTLY this format (3 lines, no other text):
Q: <question about a design decision or choice>
A: <justification in 2-4 sentences covering trade-offs and reasoning>
%s

Example:
Q: Why does this system use confidence-based scheduling instead of SM-2 spaced repetition?
A: SM-2 computes the next review date algorithmically, but users felt disconnected from the scheduling. Confidence-based puts the user in control — they rate how well they know it, and the system prioritizes low-confidence items. The trade-off is less optimal spacing, but higher engagement because users trust their own ratings.
E: SM-2 (SuperMemo 2) calculates intervals using ease factors and repetition counts. It's mathematically optimal but opaque. Confidence-based is simpler: the user says "I know this" (5) or "I'm shaky" (2), and the system sorts by that. The insight is that engagement matters more than optimal spacing for most learners.`, dc, ec)

	case TypeArchitecture:
		return fmt.Sprintf(`Generate ONE architecture trace question from the document below.
%s

Ask the user to trace a data flow, request path, or event chain through the system. "What happens when X?" or "Trace the path from A to B." Tests understanding of how components connect.

Output EXACTLY this format (3 lines, no other text):
Q: <question asking to trace a flow or path through the system>
A: <step-by-step trace in 3-6 steps>
%s

Example:
Q: Trace what happens from when the user presses 'r' on the dashboard to the first question appearing on screen.
A: 1) handleDashboard receives 'r', calls startReview(). 2) startReview sorts files by priority, inserts prereqs, interleaves domains. 3) resetSession clears counters, sets phase to phaseQuiz. 4) startFile loads the knowledge file via loadLesson cmd. 5) lessonMsg arrives, sets sourceContent and quizStep to stepLesson.
E: This is a classic event-driven flow in the Elm architecture (Model-View-Update). The key insight is that pressing a key doesn't directly show a question — it triggers a chain of commands and messages. The lesson (teach-first) always comes before the question.`, dc, ec)

	case TypeRefactor:
		return fmt.Sprintf(`Generate ONE refactoring question from the document below.
%s

Ask how the user would improve, extract, or restructure part of the system. Focus on concrete refactoring moves — not vague "make it better." Tests ability to think about code organization and design.

Output EXACTLY this format (3 lines, no other text):
Q: <question about a specific refactoring opportunity>
A: <concrete refactoring approach in 2-4 sentences>
%s

Example:
Q: How would you extract the grading logic (correct/wrong determination + confidence update + XP award) into its own package?
A: Create an internal/grading package with a GradeResult struct holding verdict, feedback, XP delta, and new confidence. Move the grading decision logic from update.go into a pure function Grade(answer, correct, qtype) → GradeResult. Keep the state mutation (writing confidence to state.json) in update.go — the grading package computes, the caller persists.
E: The key principle is separating computation from side effects. The grading logic is currently mixed with UI state updates in update.go. Extracting it makes the grading testable in isolation and reusable across quiz, challenge, and future interview modes.`, dc, ec)

	default: // flashcard
		return fmt.Sprintf(`Generate ONE flashcard question from the document below.
%s

Ask about specific, testable facts — key definitions, important numbers, concrete behaviors.

Rules:
- Ask something specific and testable — not "explain X" or "describe X"
- The answer should be concrete: a fact, a name, a short explanation (1-2 sentences max)
- Test understanding, not just whether they read the document

Output EXACTLY this format (3 lines, no other text):
Q: <question>
A: <answer>
%s

Example:
Q: What happens if main() returns while goroutines are still running?
A: All goroutines are killed immediately. The program exits without waiting for them to finish.
E: When main() returns, the Go runtime tears down everything — no cleanup, no waiting. If you have a goroutine writing to a file, it gets killed mid-write. Fix: use sync.WaitGroup to block main until workers finish, or use a context with cancel to signal graceful shutdown.`, dc, ec)
	}
}

// ChatWithHistory sends a system prompt + conversation history and returns the response.
func (c *Client) ChatWithHistory(ctx context.Context, system string, history []Message) (string, error) {
	msgs := []message{{Role: "system", Content: system}}
	for _, h := range history {
		msgs = append(msgs, message{Role: h.Role, Content: h.Content})
	}
	return c.chatMulti(ctx, msgs)
}

// StreamChunk is one token chunk from a streaming chat response.
type StreamChunk struct {
	Content  string
	Done     bool
	Err      error
	Duration time.Duration
}

// ChatWithHistoryStream sends a streaming chat request and returns chunks via channel.
func (c *Client) ChatWithHistoryStream(ctx context.Context, system string, history []Message) (<-chan StreamChunk, error) {
	msgs := []message{{Role: "system", Content: system}}
	for _, h := range history {
		msgs = append(msgs, message{Role: h.Role, Content: h.Content})
	}

	c.sem <- struct{}{}

	body := map[string]interface{}{
		"model":    c.model,
		"messages": msgs,
		"stream":   true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		<-c.sem
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(data))
	if err != nil {
		<-c.sem
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		<-c.sem
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		<-c.sem
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(b))
	}

	ch := make(chan StreamChunk)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		defer func() { <-c.sem }()

		start := time.Now()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var sr struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Done bool `json:"done"`
			}
			if err := json.Unmarshal(line, &sr); err != nil {
				continue
			}
			chunk := StreamChunk{Content: sr.Message.Content, Done: sr.Done}
			if sr.Done {
				chunk.Duration = time.Since(start)
			}
			ch <- chunk
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Err: fmt.Errorf("stream error: %w", err), Done: true}
		}
	}()

	return ch, nil
}

// Chat sends a simple system+user message pair and returns the response.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	return c.chatMulti(ctx, []message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
}

func (c *Client) chatMulti(ctx context.Context, messages []message) (string, error) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	body := chatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   false,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(b))
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", err
	}

	result := cr.Message.Content

	c.logMu.Lock()
	w := c.logger
	c.logMu.Unlock()
	if w != nil {
		var lb strings.Builder
		lb.WriteString("\n---\n")
		lb.WriteString(time.Now().Format("2006-01-02 15:04:05"))
		lb.WriteString("\n")
		for _, msg := range messages {
			lb.WriteString(fmt.Sprintf("[%s]\n%s\n\n", strings.ToUpper(msg.Role), msg.Content))
		}
		lb.WriteString("[RESPONSE]\n")
		lb.WriteString(result)
		lb.WriteString("\n")
		if _, err := fmt.Fprint(w, lb.String()); err != nil {
			fmt.Fprintf(os.Stderr, "unrot: log write failed: %v\n", err)
		}
	}

	return result, nil
}

func parseResponse(text string, qtype QuestionType) (*Question, error) {
	// Strip markdown code fences that models love to add
	text = stripCodeFences(text)
	if qtype == TypeMultiChoice || qtype == TypeOrdering {
		return parseMultiChoice(text)
	}
	return parseQA(text, qtype)
}

// stripCodeFences removes ```lang ... ``` wrappers from model output.
func stripCodeFences(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// stripOuterFence removes a single wrapping ```[lang] ... ``` if the entire response is wrapped in one.
func stripOuterFence(text string) string {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return text
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.HasPrefix(first, "```") && last == "```" {
		inner := strings.Join(lines[1:len(lines)-1], "\n")
		return strings.TrimSpace(inner)
	}
	return text
}

func parseQA(text string, qtype QuestionType) (*Question, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var qLines, eLines []string
	var a string
	section := "" // tracks which section we're accumulating into

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "Q:"); ok {
			section = "q"
			qLines = append(qLines, strings.TrimSpace(after))
		} else if after, ok := strings.CutPrefix(trimmed, "A:"); ok {
			section = "a"
			a = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(trimmed, "E:"); ok {
			section = "e"
			eLines = append(eLines, strings.TrimSpace(after))
		} else if trimmed != "" {
			switch section {
			case "q":
				qLines = append(qLines, line)
			case "e":
				eLines = append(eLines, trimmed)
			}
		}
	}

	q := strings.TrimSpace(strings.Join(qLines, "\n"))
	e := strings.TrimSpace(strings.Join(eLines, " "))
	if q == "" || a == "" {
		return nil, fmt.Errorf("failed to parse Q/A from ollama response:\n%s", text)
	}

	// Fix fill-blank questions where the LLM forgot to blank out the answer.
	// Replace the answer word in the question with ___ so the user sees a blank.
	if qtype == TypeFillBlank && !strings.Contains(q, "___") {
		lq := strings.ToLower(q)
		la := strings.ToLower(a)
		// Try exact match first
		if idx := strings.Index(lq, la); idx >= 0 {
			q = q[:idx] + "___" + q[idx+len(a):]
		} else {
			// Try bold/backtick-wrapped variants: **answer**, *answer*, `answer`
			for _, wrap := range []string{"**%s**", "*%s*", "`%s`"} {
				wrapped := fmt.Sprintf(wrap, a)
				if idx := strings.Index(strings.ToLower(q), strings.ToLower(wrapped)); idx >= 0 {
					q = q[:idx] + "___" + q[idx+len(wrapped):]
					break
				}
			}
		}
	}

	return &Question{Type: qtype, Text: q, Answer: a, Explanation: e}, nil
}

func parseMultiChoice(text string) (*Question, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var q string
	var eLines []string
	var options []string
	correctIdx := -1
	section := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "Q:"); ok {
			section = "q"
			q = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(strings.ToUpper(line), "ANSWER:"); ok {
			section = "answer"
			letter := strings.TrimSpace(after)
			if len(letter) > 0 {
				idx := int(strings.ToUpper(letter)[0] - 'A')
				if idx >= 0 && idx < 4 {
					correctIdx = idx
				}
			}
		} else if after, ok := strings.CutPrefix(line, "E:"); ok {
			section = "e"
			eLines = append(eLines, strings.TrimSpace(after))
		} else if section == "e" && line != "" {
			eLines = append(eLines, line)
		} else {
			// Parse A) B) C) D) options — case insensitive
			for i, prefix := range []string{"A)", "B)", "C)", "D)"} {
				lowerPrefix := strings.ToLower(prefix)
				if after, ok := strings.CutPrefix(line, prefix); ok {
					section = "opt"
					for len(options) <= i {
						options = append(options, "")
					}
					options[i] = strings.TrimSpace(after)
					break
				} else if after, ok := strings.CutPrefix(line, lowerPrefix); ok {
					section = "opt"
					for len(options) <= i {
						options = append(options, "")
					}
					options[i] = strings.TrimSpace(after)
					break
				}
			}
		}
	}

	e := strings.TrimSpace(strings.Join(eLines, " "))

	if q == "" || len(options) < 4 || correctIdx < 0 {
		return nil, fmt.Errorf("failed to parse multiple choice from ollama response:\n%s", text)
	}

	return &Question{
		Type:        TypeMultiChoice,
		Text:        q,
		Answer:      options[correctIdx],
		Options:     options,
		CorrectIdx:  correctIdx,
		Explanation: e,
	}, nil
}

// GenerateChallenge creates a standalone coding challenge (not tied to a knowledge file).
func (c *Client) GenerateChallenge(ctx context.Context, domain string, diff Difficulty) (*Challenge, error) {
	domainCtx := "any programming language (vary between Go, Python, JavaScript, Rust)"
	if domain != "" {
		domainCtx = fmt.Sprintf("focused on %s", domain)
	}

	system := fmt.Sprintf(`You are a coding challenge generator for a developer practice tool.
Generate ONE standalone coding challenge — %s.
%s

Vary the challenge type across calls:
- Syntax: "Write the correct syntax for..." (quick, specific)
- Mini function: "Write a function that..." (medium, focused)
- Algorithm: "Implement an efficient solution for..." (longer, analytical)
- Debug: "Fix the bug in this code..." (reading + fixing)
- Code output: "What does this output? Then rewrite it to..." (tracing + coding)

Output EXACTLY this format:
TITLE: <short title, 3-8 words>
LANG: <language name>
---
<clear problem description>
<include input/output examples where helpful>
<state any constraints or requirements>
===
<2-8 sentences of relevant syntax or concept knowledge — key functions, idioms, or patterns the solver should know. Not the answer, just enough context to jog memory.>

Rules:
- Problems should be solvable in 5-30 lines of code
- Be specific — include concrete examples with expected input/output
- For algorithm problems, state expected time/space complexity
- Do NOT wrap the entire output in markdown code fences
- Code examples within the description should use plain text (no backticks)`, domainCtx, difficultyClause(diff))

	resp, err := c.Chat(ctx, system, "Generate a challenge.")
	if err != nil {
		return nil, err
	}
	return parseChallenge(resp, diff)
}

// GenerateChallengeFromChat creates a challenge based on a conversational topic clarification.
func (c *Client) GenerateChallengeFromChat(ctx context.Context, topic string, history []Message, diff Difficulty) (*Challenge, error) {
	system := fmt.Sprintf(`You are a coding challenge generator for a developer practice tool.
Based on the conversation below, generate ONE standalone coding challenge tailored to what the user wants to practice.
%s

Vary the challenge type:
- Syntax: "Write the correct syntax for..." (quick, specific)
- Mini function: "Write a function that..." (medium, focused)
- Algorithm: "Implement an efficient solution for..." (longer, analytical)
- Debug: "Fix the bug in this code..." (reading + fixing)
- Code output: "What does this output? Then rewrite it to..." (tracing + coding)

Output EXACTLY this format:
TITLE: <short title, 3-8 words>
LANG: <language name>
---
<clear problem description>
<include input/output examples where helpful>
<state any constraints or requirements>
===
<2-8 sentences of relevant syntax or concept knowledge — key functions, idioms, or patterns the solver should know. Not the answer, just enough context to jog memory.>

Rules:
- All information needed to solve the answer should be in the problem output, not to give it away, but you shouldn't *NEED* to talk to the chat or look things up to solve it if you just read the problem well enough. Even examples of things that AREN'T the answer to help demonstrate syntax etc are fine.
- Problems should be solvable in 5-30 lines of code
- Be specific — include concrete examples with expected input/output
- For algorithm problems, state expected time/space complexity
- Do NOT wrap the entire output in markdown code fences
- Code examples within the description should use plain text (no backticks)`, difficultyClause(diff))

	msgs := make([]Message, 0, len(history)+1)
	msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf("I want to practice: %s", topic)})
	for _, h := range history {
		msgs = append(msgs, Message{Role: h.Role, Content: h.Content})
	}
	msgs = append(msgs, Message{Role: "user", Content: "Generate the challenge now."})

	resp, err := c.ChatWithHistory(ctx, system, msgs)
	if err != nil {
		return nil, err
	}
	return parseChallenge(resp, diff)
}

// AnswerGrade is ollama's evaluation of a typed answer.
type AnswerGrade struct {
	Correct  bool
	Feedback string
}

// GradeFinishCode evaluates a code-completion answer against the expected line.
// snippet is the full code context (with // ??? placeholder), expectedLine is the model answer.
func (c *Client) GradeFinishCode(ctx context.Context, snippet, expectedLine, userAnswer string) (*AnswerGrade, error) {
	system := fmt.Sprintf(`You are grading a code-completion quiz question.

The code snippet (shown to the user with // ??? as the blank):
%s

The model answer (INTERNAL — NEVER reveal this):
%s

Rules:
- Accept ANY implementation that is semantically equivalent or achieves the same correct result.
- Do NOT require exact syntax match — different but equally valid code is CORRECT.
- Be strict about semantic correctness: wrong behavior, missing keywords, or broken logic = WRONG.
- Small style differences (spacing, variable names) are fine if behavior is correct.

Output EXACTLY this format:
CORRECT: <yes/no>
---
<2-3 sentences of feedback:
- If CORRECT: explain what their code does and why it works in this context. Reinforce the concept.
- If WRONG: describe what the code they wrote would actually do (or fail to do) in context. Point at the specific concept or keyword they need — do NOT name or quote the correct answer. Give a directional code hint (e.g. "think about how X prevents Y" or "you need a mechanism that Z").>

CRITICAL: Never reveal the model answer, never say "the answer is", never quote or paraphrase the expected line.`, snippet, expectedLine)

	user := fmt.Sprintf("User's code: %s", userAnswer)

	resp, err := c.Chat(ctx, system, user)
	if err != nil {
		return nil, err
	}
	return parseAnswerGrade(resp)
}

// GradeAnswer evaluates a typed answer against the correct answer.
func (c *Client) GradeAnswer(ctx context.Context, question, correctAnswer, userAnswer string) (*AnswerGrade, error) {
	system := fmt.Sprintf(`You are grading a quiz answer. The correct answer is: %s

Rules:
- If the user's answer matches the correct answer (exactly or close enough), mark CORRECT: yes. Do not overthink this.
- Accept substantially equivalent answers even if worded differently.
- Be lenient on completeness, strict on factual accuracy.
- If wrong: do NOT reveal the correct answer in feedback. Point at what's off and guide them toward the right concept.

Output EXACTLY:
CORRECT: <yes/no>
---
<2-3 sentences: if correct, reinforce why it's right; if wrong, explain what's off without naming the answer>`, correctAnswer)

	user := fmt.Sprintf("Question: %s\n\nUser's answer: %s", question, userAnswer)

	resp, err := c.Chat(ctx, system, user)
	if err != nil {
		return nil, err
	}
	return parseAnswerGrade(resp)
}

func parseAnswerGrade(text string) (*AnswerGrade, error) {
	text = strings.TrimSpace(text)
	text = stripCodeFences(text)
	grade := &AnswerGrade{}

	lines := strings.Split(text, "\n")
	feedbackStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "CORRECT:"); ok {
			val := strings.ToLower(strings.TrimSpace(after))
			grade.Correct = val == "yes" || val == "true"
		} else if trimmed == "---" {
			feedbackStart = i + 1
			break
		}
	}

	if feedbackStart >= 0 && feedbackStart < len(lines) {
		grade.Feedback = strings.TrimSpace(strings.Join(lines[feedbackStart:], "\n"))
	}
	return grade, nil
}

// GradeChallenge evaluates a user's code submission against a challenge.
func (c *Client) GradeChallenge(ctx context.Context, challenge *Challenge, code string) (*ChallengeGrade, error) {
	system := `You are grading a coding challenge submission. You cannot execute the code — evaluate by reading carefully.

Score on these dimensions:
- Correctness (50pts): Does it handle typical inputs? Does it handle edge cases (null, empty, overflow, negatives)?
- Efficiency (30pts): Is the approach optimal or is there obvious waste?
- Code quality (20pts): Is it clean, readable, idiomatic for the language?

CORRECT: yes = clearly correct for all typical inputs. no = wrong logic or definitely broken.

Feedback rules:
- Be specific about WHAT is wrong (not just "this could fail")
- Name the specific input or condition that would break it
- If wrong: explain what the code does vs what it should do, then give a directional hint — do NOT give the solution
- If correct but improvable: say what's good, then point out the one most impactful improvement
- 3-6 sentences, direct and technical

Output EXACTLY this format:
SCORE: <0-100>
CORRECT: <yes/no>
EFFICIENCY: <optimal/acceptable/suboptimal>
---
<feedback>`

	user := fmt.Sprintf("Challenge: %s\n\n%s\n\nSubmitted code:\n%s", challenge.Title, challenge.Description, code)

	resp, err := c.Chat(ctx, system, user)
	if err != nil {
		return nil, err
	}
	return parseChallengeGrade(resp)
}

func parseChallenge(text string, diff Difficulty) (*Challenge, error) {
	text = strings.TrimSpace(text)
	text = stripCodeFences(text)
	ch := &Challenge{Difficulty: diff}

	lines := strings.Split(text, "\n")
	descStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "TITLE:"); ok {
			ch.Title = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(trimmed, "LANG:"); ok {
			ch.Language = strings.TrimSpace(after)
		} else if trimmed == "---" {
			descStart = i + 1
			break
		}
	}

	if descStart >= 0 && descStart < len(lines) {
		body := lines[descStart:]
		conceptStart := -1
		for i, line := range body {
			if strings.TrimSpace(line) == "===" {
				conceptStart = i
				break
			}
		}
		if conceptStart >= 0 {
			ch.Description = strings.TrimSpace(strings.Join(body[:conceptStart], "\n"))
			ch.Concept = strings.TrimSpace(strings.Join(body[conceptStart+1:], "\n"))
		} else {
			ch.Description = strings.TrimSpace(strings.Join(body, "\n"))
		}
	} else if ch.Title != "" {
		// No --- separator — everything after LANG line is description
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "LANG:") {
				if i+1 < len(lines) {
					ch.Description = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
				}
				break
			}
		}
	}

	if ch.Title == "" || ch.Description == "" {
		return nil, fmt.Errorf("failed to parse challenge from ollama response:\n%s", text)
	}
	if ch.Language == "" {
		ch.Language = "General"
	}
	return ch, nil
}

func parseChallengeGrade(text string) (*ChallengeGrade, error) {
	text = strings.TrimSpace(text)
	text = stripCodeFences(text)
	grade := &ChallengeGrade{}

	lines := strings.Split(text, "\n")
	feedbackStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "SCORE:"); ok {
			fmt.Sscanf(strings.TrimSpace(after), "%d", &grade.Score)
		} else if after, ok := strings.CutPrefix(trimmed, "CORRECT:"); ok {
			val := strings.ToLower(strings.TrimSpace(after))
			grade.Correct = val == "yes" || val == "true"
		} else if after, ok := strings.CutPrefix(trimmed, "EFFICIENCY:"); ok {
			grade.Efficiency = strings.TrimSpace(after)
		} else if trimmed == "---" {
			feedbackStart = i + 1
			break
		}
	}

	if feedbackStart >= 0 && feedbackStart < len(lines) {
		grade.Feedback = strings.TrimSpace(strings.Join(lines[feedbackStart:], "\n"))
	}

	// Allow partial parses — at minimum we need some feedback
	if grade.Score == 0 && grade.Feedback == "" {
		return nil, fmt.Errorf("failed to parse challenge grade from ollama response:\n%s", text)
	}
	return grade, nil
}

// EnrichFile analyzes a knowledge file and returns its difficulty tier and suggested prerequisites.
// difficulty is "easy", "medium", or "hard" relative to other topics in the same domain.
// connections are "domain/slug" references from the index that this file genuinely depends on.
func (c *Client) EnrichFile(ctx context.Context, fileContent, indexContent, domain, slug string) (difficulty string, connections []string, err error) {
	system := `You are a knowledge graph assistant. Analyze the given knowledge file and respond with:
1. A difficulty rating relative to other topics in the same domain:
   - easy: fundamentals, definitions, basic syntax — what you need to know first
   - medium: applying concepts, patterns, comparisons
   - hard: edge cases, advanced patterns, gotchas, deep internals
2. Any genuine prerequisites from the knowledge index — only files the reader must understand first.

Respond in EXACTLY this format, nothing else:
difficulty: <easy|medium|hard>
requires: domain/slug
requires: domain/slug

If no prerequisites, just output the difficulty line.`

	user := fmt.Sprintf("Domain: %s\nFile: %s\n\n--- FILE CONTENT ---\n%s\n\n--- KNOWLEDGE INDEX (domain → slug — description) ---\n%s",
		domain, slug, fileContent, indexContent)

	resp, err := c.Chat(ctx, system, user)
	if err != nil {
		return "medium", nil, err
	}

	difficulty = "medium"
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "difficulty:"); ok {
			d := strings.TrimSpace(after)
			if d == "easy" || d == "medium" || d == "hard" {
				difficulty = d
			}
		} else if after, ok := strings.CutPrefix(line, "requires:"); ok {
			ref := strings.TrimSpace(after)
			// Skip self-reference
			if ref != "" && ref != domain+"/"+slug {
				connections = append(connections, ref)
			}
		}
	}
	return difficulty, connections, nil
}

// SubsystemProposal is a proposed subsystem with its relevant source files.
type SubsystemProposal struct {
	Slug  string
	Desc  string
	Files []string
}

// ProposeSubsystems asks ollama to suggest subsystems with file mappings in ONE call.
func (c *Client) ProposeSubsystems(ctx context.Context, archContext, fileTree string) ([]SubsystemProposal, error) {
	system := `You are analyzing a software project to propose subsystems worth documenting for interview prep.
Each subsystem is a logical grouping of functionality that maps to real files in the codebase.

Rules:
- Propose 5-8 subsystems, ordered by importance (fewer is better — only the most interesting)
- Focus on things a developer would need to explain in an interview
- Prioritize: core architecture, interesting patterns, non-obvious design decisions
- For each subsystem, list the 1-6 most relevant source files from the file tree

Output format (EXACTLY this, nothing else):
slug — description
  files: path/to/file1, path/to/file2

slug — description
  files: path/to/file1, path/to/file2`

	userMsg := "Project architecture:\n\n" + archContext
	if fileTree != "" {
		userMsg += "\n\nFile tree:\n" + fileTree
	}
	resp, err := c.Chat(ctx, system, userMsg)
	if err != nil {
		return nil, err
	}

	var proposals []SubsystemProposal
	lines := strings.Split(resp, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		// Strip numbering
		for j, ch := range line {
			if ch == '.' || ch == ')' {
				if j > 0 && j <= 2 {
					line = strings.TrimSpace(line[j+1:])
				}
				break
			}
			if ch < '0' || ch > '9' {
				break
			}
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(strings.ToLower(line), "files:") {
			continue
		}

		// Parse slug — description
		slug, desc := line, ""
		if idx := strings.Index(line, " — "); idx > 0 {
			slug, desc = line[:idx], line[idx+len(" — "):]
		} else if idx := strings.Index(line, " - "); idx > 0 {
			slug, desc = line[:idx], line[idx+3:]
		}
		slug = strings.TrimSpace(slug)

		// Validate slug
		hasLetter, valid := false, true
		for _, ch := range slug {
			if ch >= 'A' && ch <= 'Z' {
				valid = false
				break
			}
			if ch >= 'a' && ch <= 'z' {
				hasLetter = true
			}
			if ch == ' ' {
				valid = false
				break
			}
		}
		if !valid || !hasLetter {
			continue
		}

		// Look for files: line immediately after
		var files []string
		if i+1 < len(lines) {
			nextLine := strings.TrimSpace(lines[i+1])
			if idx := strings.Index(strings.ToLower(nextLine), "files:"); idx >= 0 {
				fileList := strings.TrimSpace(nextLine[idx+6:])
				for _, f := range strings.Split(fileList, ",") {
					f = strings.TrimSpace(f)
					if f != "" {
						files = append(files, f)
					}
				}
				i++ // skip the files line
			}
		}

		proposals = append(proposals, SubsystemProposal{Slug: slug, Desc: desc, Files: files})
	}

	if len(proposals) > 9 {
		proposals = proposals[:9]
	}
	return proposals, nil
}



// ExtractFileNotes reads a source file and extracts key information for a subsystem,
// building on accumulated notes from previously processed files.
func (c *Client) ExtractFileNotes(ctx context.Context, fileName, fileContent, runningNotes, subsystem string) (string, error) {
	system := fmt.Sprintf(`You are analyzing source files for the "%s" subsystem, one file at a time.
Your job: extract what matters for understanding this code AND for interview prep.

Rules:
- Be DENSE and SPECIFIC — function names, struct fields, state transitions, not vague summaries
- Identify design patterns used (state machine, fan-out, builder, pub-sub, observer, etc)
- Note algorithmic choices and their time/space complexity
- Flag trade-offs: why this approach over alternatives?
- Tag interview-relevant concepts (concurrency, caching, error handling, serialization, etc)
- Note connections to things found in previously analyzed files
- Include short code snippets for non-obvious logic (5-10 lines max each)
- Call out implicit assumptions, ordering dependencies, edge cases
- Output as bullet points organized by concern
- ~30-60 lines of notes per file — enough to write a knowledge doc from later
- If this file is less relevant than expected, say so briefly and move on`, subsystem)

	prompt := fmt.Sprintf("File: %s\n\n```\n%s\n```", fileName, fileContent)
	if runningNotes != "" {
		prompt = fmt.Sprintf("Notes from previously analyzed files:\n%s\n\n---\n\n%s", runningNotes, prompt)
	}
	prompt += fmt.Sprintf("\n\nExtract the key information from %s for the %s subsystem. Build on the notes above — reference connections, add missing context, note contradictions.", fileName, subsystem)

	resp, err := c.Chat(ctx, system, prompt)
	if err != nil {
		return "", err
	}
	return stripOuterFence(resp), nil
}

// GenerateProjectFromNotes creates a knowledge doc from accumulated file analysis notes.
// Focused on interview prep: ties code to patterns, trade-offs, and questions an interviewer would ask.
func (c *Client) GenerateProjectFromNotes(ctx context.Context, projectName, subsystem, archContext, accumulatedNotes string, chatHistory []Message) (string, error) {
	system := fmt.Sprintf(`You are documenting the "%s" subsystem of the "%s" project for the developer who built it.
You have accumulated analysis notes from reading the actual source files, plus architecture context.

The goal: prepare the developer to explain this code in a technical interview. They built it — help them articulate WHY and HOW.

Rules:
- Start with: # %s — %s
- Reference specific function/type names from the source analysis
- Every fact should be something an interviewer could ask about
- Use these sections:

## Overview
1-3 sentences: what this subsystem does and why it exists

## Architecture & Patterns
- Name each design pattern used (state machine, fan-out, observer, etc)
- Show WHERE it's used (specific functions/types)
- Explain the trade-off: "X pattern because Y. Alternative was Z but that would mean..."

## Key Implementation Details
- Data structures, state transitions, control flow
- Specific function/type names with file references
- Show key code constructs inline (brief)

## Interview Angles
- 3-5 questions an interviewer might ask about this subsystem
- Format each as: "Why did you choose X over Y?" → brief answer
- Include: scaling questions, failure mode questions, design justification questions

## Gotchas
- Edge cases, implicit assumptions, ordering dependencies
- Things that would trip you up coming back to this code

## Connections
- difficulty: medium
- requires: domain/slug (only if a specific prerequisite from the knowledge base applies — omit if none)

- Keep it 60-100 lines — dense, specific, quizzable
- NO markdown code fences wrapping the whole document`, subsystem, projectName, projectName, subsystem)

	msgs := make([]Message, 0, len(chatHistory)+3)
	if archContext != "" {
		msgs = append(msgs, Message{Role: "user", Content: "Architecture context:\n\n" + archContext})
		msgs = append(msgs, Message{Role: "assistant", Content: "Got it. I have the project architecture context."})
	}
	msgs = append(msgs, chatHistory...)
	msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf("Here are my accumulated analysis notes from reading the source files for the %s subsystem:\n\n%s\n\nNow generate the knowledge document.", subsystem, accumulatedNotes)})

	resp, err := c.ChatWithHistory(ctx, system, msgs)
	if err != nil {
		return "", err
	}
	return stripOuterFence(resp), nil
}

