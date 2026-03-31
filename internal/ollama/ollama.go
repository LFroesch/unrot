package ollama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
)

type Client struct {
	host  string
	model string
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
	return &Client{host: host, model: model}
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
var AllTypes = []QuestionType{TypeFlashcard, TypeExplain, TypeFillBlank, TypeFinishCode, TypeMultiChoice, TypeCompare, TypeScenario, TypeOrdering, TypeCodeOutput, TypeDebug}

// GenerateQuestion generates a question of the given type. If qtype is -1, picks randomly.
func (c *Client) GenerateQuestion(content, filename string, qtype QuestionType, diff Difficulty) (*Question, error) {
	if qtype < 0 {
		qtype = AllTypes[rand.Intn(len(AllTypes))]
	}

	system := promptFor(qtype, diff)
	user := fmt.Sprintf("Document: %s\n\n%s", filename, content)

	resp, err := c.Chat(system, user)
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
func (c *Client) GenerateKnowledge(topic string, chatHistory []Message, existingFiles []string) (content, domain, slug string, err error) {
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

	resp, err2 := c.ChatWithHistory(system, msgs)
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
func (c *Client) UpdateKnowledge(topic string, chatHistory []Message, existingContent string) (string, error) {
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

	return c.ChatWithHistory(system, msgs)
}

func difficultyClause(d Difficulty) string {
	shared := `
IMPORTANT:
- Do NOT copy sentences from the document verbatim as a question — rephrase and test understanding
- Focus on a DIFFERENT aspect each time — vary which section/concept you target
- The question should require THINKING, not just pattern-matching against the text`

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
- Keep it approachable — one clear concept per question
- The answer should be something concrete, not a vague explanation` + shared
	}
}

func explanationClause() string {
	return `E: <explanation — TEACH the concept in 2-4 sentences. Include a concrete example (code, input→output, or before→after). Explain WHY, not just WHAT. Must be factually correct. Can span multiple lines.>`
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
func (c *Client) ChatWithHistory(system string, history []Message) (string, error) {
	msgs := []message{{Role: "system", Content: system}}
	for _, h := range history {
		msgs = append(msgs, message{Role: h.Role, Content: h.Content})
	}
	return c.chatMulti(msgs)
}

// Chat sends a simple system+user message pair and returns the response.
func (c *Client) Chat(system, user string) (string, error) {
	return c.chatMulti([]message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
}

func (c *Client) chatMulti(messages []message) (string, error) {
	body := chatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   false,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(c.host+"/api/chat", "application/json", bytes.NewReader(data))
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
	return cr.Message.Content, nil
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
