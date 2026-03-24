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
		model = "qwen2.5:3b"
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
	default:
		return "flashcard"
	}
}

type Question struct {
	Type       QuestionType
	Text       string
	Answer     string
	Options    []string // for multiple choice
	CorrectIdx int      // for multiple choice
}

// AllTypes returns the full set of question types for random selection.
var AllTypes = []QuestionType{TypeFlashcard, TypeExplain, TypeFillBlank, TypeFinishCode, TypeMultiChoice}

// GenerateQuestion generates a question of the given type. If qtype is -1, picks randomly.
func (c *Client) GenerateQuestion(content, filename string, qtype QuestionType) (*Question, error) {
	if qtype < 0 {
		qtype = AllTypes[rand.Intn(len(AllTypes))]
	}

	system := promptFor(qtype)
	user := fmt.Sprintf("Document: %s\n\n%s", filename, content)

	resp, err := c.chat(system, user)
	if err != nil {
		return nil, err
	}

	return parseResponse(resp, qtype)
}

// GenerateFlashcard is kept for backward compat.
func (c *Client) GenerateFlashcard(content, filename string) (*Question, error) {
	return c.GenerateQuestion(content, filename, TypeFlashcard)
}

func promptFor(t QuestionType) string {
	switch t {
	case TypeExplain:
		return `You are a quiz generator. Given a knowledge document, generate ONE "explain" question.
Rules:
- Ask the user to explain a concept from the document in their own words
- The answer should be a concise model explanation (2-3 sentences)
- Output EXACTLY in this format, no other text:
Q: <question>
A: <answer>`

	case TypeFillBlank:
		return `You are a quiz generator. Given a knowledge document, generate ONE fill-in-the-blank question.
Rules:
- Take a key sentence from the document and replace one important term with ___
- The answer is the missing term
- Output EXACTLY in this format, no other text:
Q: <sentence with ___ blank>
A: <missing word or short phrase>`

	case TypeFinishCode:
		return `You are a quiz generator. Given a knowledge document, generate ONE finish-the-code question.
Rules:
- Show a short code snippet (2-5 lines) from or inspired by the document with a key part replaced by ???
- The answer is the missing code
- If the document has no code, generate a conceptual snippet that applies the knowledge
- Output EXACTLY in this format, no other text:
Q: <code with ??? placeholder>
A: <the missing code>`

	case TypeMultiChoice:
		return `You are a quiz generator. Given a knowledge document, generate ONE multiple choice question.
Rules:
- 4 options labeled A) B) C) D)
- Exactly one correct answer
- Distractors should be plausible but clearly wrong
- Output EXACTLY in this format, no other text:
Q: <question>
A) <option>
B) <option>
C) <option>
D) <option>
ANSWER: <letter>`

	default: // flashcard
		return `You are a quiz generator. Given a knowledge document, generate ONE flashcard-style question and answer.
Rules:
- Question should test understanding, not just recall
- Answer should be 1-3 sentences max
- Output EXACTLY in this format, no other text:
Q: <question>
A: <answer>`
	}
}

func (c *Client) EvalAnswer(question, expected, actual string) (string, error) {
	system := `You are evaluating a quiz answer. Compare the user's answer to the expected answer.
Be encouraging but honest. Output 1-2 sentences of feedback. Start with "Correct!" or "Not quite."`

	user := fmt.Sprintf("Question: %s\nExpected: %s\nUser answered: %s", question, expected, actual)

	return c.chat(system, user)
}

func (c *Client) chat(system, user string) (string, error) {
	body := chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: false,
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
	if qtype == TypeMultiChoice {
		return parseMultiChoice(text)
	}
	return parseQA(text, qtype)
}

func parseQA(text string, qtype QuestionType) (*Question, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var q, a string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "Q:"); ok {
			q = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "A:"); ok {
			a = strings.TrimSpace(after)
		}
	}
	if q == "" || a == "" {
		return nil, fmt.Errorf("failed to parse Q/A from ollama response:\n%s", text)
	}
	return &Question{Type: qtype, Text: q, Answer: a}, nil
}

func parseMultiChoice(text string) (*Question, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var q string
	var options []string
	correctIdx := -1

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "Q:"); ok {
			q = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "ANSWER:"); ok {
			letter := strings.TrimSpace(after)
			if len(letter) > 0 {
				idx := int(strings.ToUpper(letter)[0] - 'A')
				if idx >= 0 && idx < 4 {
					correctIdx = idx
				}
			}
		} else {
			// Parse A) B) C) D) options
			for i, prefix := range []string{"A)", "B)", "C)", "D)"} {
				if after, ok := strings.CutPrefix(line, prefix); ok {
					for len(options) <= i {
						options = append(options, "")
					}
					options[i] = strings.TrimSpace(after)
					break
				}
			}
		}
	}

	if q == "" || len(options) < 4 || correctIdx < 0 {
		return nil, fmt.Errorf("failed to parse multiple choice from ollama response:\n%s", text)
	}

	return &Question{
		Type:       TypeMultiChoice,
		Text:       q,
		Answer:     options[correctIdx],
		Options:    options,
		CorrectIdx: correctIdx,
	}, nil
}
