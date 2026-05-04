package ollama

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Sample knowledge content for testing — real files from the Second Brain.
var sampleGoroutines = `A goroutine is a lightweight thread of execution managed by the Go runtime, not the OS. Costs ~2KB of stack (vs ~1MB for OS threads) and multiplexed onto OS threads by the Go scheduler.

## How it works

` + "```go" + `
go doWork()          // fire-and-forget
go func() {         // inline anonymous goroutine
    fmt.Println("hi")
}()
` + "```" + `

- The Go scheduler uses an M:N model — M goroutines mapped to N OS threads
- Scheduler components: **G** (goroutine), **M** (OS thread), **P** (processor/context)
- Goroutines yield at function calls, channel ops, and runtime checkpoints — not preemptive in the traditional sense

## When to use

- I/O-bound work (HTTP handlers, file reads, DB queries)
- Fan-out patterns — spawn N workers to process items concurrently
- Background tasks (timers, health checks, polling)

## Gotchas

- **No return values** — use channels or shared state to get results back
- **Leaked goroutines** — if a goroutine blocks forever (e.g. channel with no reader), it leaks memory
- main() exiting kills all goroutines — use sync.WaitGroup or channel signals to wait
- **Race conditions** — shared memory needs mutexes or channels to be safe`

var sampleDocker = `Containers share the host kernel — they are NOT VMs. A container is just a process with isolated namespaces (pid, net, mnt, user) and resource limits (cgroups).

## Key concepts

- **Image**: read-only template built from a Dockerfile. Layers are cached and shared.
- **Container**: running instance of an image with its own writable layer.
- **Volume**: persistent storage that survives container restarts. Mount with -v host:container.
- **Network**: containers get their own network namespace. Bridge (default), host, none, or overlay (Swarm).

## Common commands

` + "```bash" + `
docker build -t myapp .              # build image from Dockerfile
docker run -d -p 8080:80 myapp       # run detached, map ports
docker exec -it <id> sh              # shell into running container
docker compose up -d                 # start all services in compose file
docker system prune -a               # nuke everything unused
` + "```" + `

## Dockerfile best practices

- Use multi-stage builds to keep images small
- COPY before RUN to leverage layer caching (dependencies first, code second)
- Use .dockerignore to exclude node_modules, .git, etc.
- Don't run as root — use USER directive
- Pin base image versions (golang:1.22 not golang:latest)`

// TestQuestionQuality generates questions for each type+difficulty combo and prints them.
// Run with: go test -v -run TestQuestionQuality -count=1 ./internal/ollama/
// Set UNROT_MODEL to test different models, e.g.: UNROT_MODEL=qwen2.5:7b go test -v ...
func TestQuestionQuality(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New("")
	t.Logf("Model: %s\n", c.model)

	cases := []struct {
		name    string
		content string
		file    string
	}{
		{"goroutines", sampleGoroutines, "knowledge/go/goroutines.md"},
		{"docker", sampleDocker, "knowledge/docker/containers.md"},
	}

	types := []struct {
		name string
		qt   QuestionType
	}{
		{"flashcard", TypeFlashcard},
		{"explain", TypeExplain},
		{"fill-blank", TypeFillBlank},
		{"finish-code", TypeFinishCode},
		{"multi-choice", TypeMultiChoice},
		{"compare", TypeCompare},
		{"scenario", TypeScenario},
		{"ordering", TypeOrdering},
		{"code-output", TypeCodeOutput},
		{"debug", TypeDebug},
	}

	diffs := []Difficulty{DiffBasic, DiffIntermediate, DiffAdvanced}

	for _, tc := range cases {
		for _, qt := range types {
			for _, diff := range diffs {
				name := fmt.Sprintf("%s/%s/%s", tc.name, qt.name, diff)
				t.Run(name, func(t *testing.T) {
					q, err := c.GenerateQuestion(context.Background(), tc.content, tc.file, qt.qt, diff)
					if err != nil {
						t.Fatalf("GenerateQuestion failed: %v", err)
					}

					t.Logf("\n"+
						"━━━ %s | %s | %s ━━━\n"+
						"Q: %s\n"+
						"A: %s\n",
						tc.name, qt.name, diff, q.Text, q.Answer)

					if q.Explanation != "" {
						t.Logf("E: %s", q.Explanation)
					}
					if len(q.Options) > 0 {
						for i, opt := range q.Options {
							marker := "  "
							if i == q.CorrectIdx {
								marker = "✓ "
							}
							t.Logf("  %s%c) %s", marker, 'A'+i, opt)
						}
					}

					// Basic structural checks
					if q.Text == "" {
						t.Error("FAIL: empty question text")
					}
					if q.Answer == "" {
						t.Error("FAIL: empty answer")
					}
					if qt.qt == TypeMultiChoice {
						if len(q.Options) != 4 {
							t.Errorf("FAIL: expected 4 options, got %d", len(q.Options))
						}
						if q.CorrectIdx < 0 || q.CorrectIdx > 3 {
							t.Errorf("FAIL: bad correct index %d", q.CorrectIdx)
						}
					}
					if qt.qt == TypeFillBlank && !strings.Contains(q.Text, "___") {
						t.Error("FAIL: fill-blank question missing ___")
					}
					if qt.qt == TypeFinishCode && !strings.Contains(q.Text, "???") && !strings.Contains(q.Text, "// ???") {
						t.Error("FAIL: finish-code question missing ???")
					}
					if q.Explanation == "" {
						t.Error("WARN: no explanation generated")
					}
				})
			}
		}
	}
}

// TestParseEdgeCases tests parsing robustness with messy model outputs.
func TestParseEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		qtype   QuestionType
		wantErr bool
		checkQ  func(*Question) error
	}{
		{
			name:  "clean flashcard",
			input: "Q: What is a goroutine?\nA: A lightweight thread managed by Go runtime.\nE: Much cheaper than OS threads.",
			qtype: TypeFlashcard,
			checkQ: func(q *Question) error {
				if q.Text != "What is a goroutine?" {
					return fmt.Errorf("bad text: %q", q.Text)
				}
				if q.Answer != "A lightweight thread managed by Go runtime." {
					return fmt.Errorf("bad answer: %q", q.Answer)
				}
				return nil
			},
		},
		{
			name:  "extra whitespace and blank lines",
			input: "\n\n  Q:  What is a goroutine?  \n\n  A:  Lightweight thread.  \n  E:  Cheap.  \n\n",
			qtype: TypeFlashcard,
			checkQ: func(q *Question) error {
				if q.Text != "What is a goroutine?" {
					return fmt.Errorf("bad text: %q", q.Text)
				}
				return nil
			},
		},
		{
			name:    "missing answer",
			input:   "Q: What is Docker?\n",
			qtype:   TypeFlashcard,
			wantErr: true,
		},
		{
			name:    "missing question",
			input:   "A: Some answer\nE: Some explanation",
			qtype:   TypeFlashcard,
			wantErr: true,
		},
		{
			name:  "model adds preamble before Q/A",
			input: "Here's a question for you:\n\nQ: What are cgroups?\nA: Resource limits for containers.\nE: Controls CPU, memory, etc.",
			qtype: TypeFlashcard,
			checkQ: func(q *Question) error {
				if q.Text != "What are cgroups?" {
					return fmt.Errorf("bad text: %q", q.Text)
				}
				return nil
			},
		},
		{
			name: "clean multiple choice",
			input: `Q: Which component is NOT part of the Go scheduler?
A) G (goroutine)
B) M (OS thread)
C) P (processor)
D) S (semaphore)
ANSWER: D
E: The scheduler uses G, M, P — there is no S component.`,
			qtype: TypeMultiChoice,
			checkQ: func(q *Question) error {
				if q.CorrectIdx != 3 {
					return fmt.Errorf("expected correct=3, got %d", q.CorrectIdx)
				}
				if len(q.Options) != 4 {
					return fmt.Errorf("expected 4 options, got %d", len(q.Options))
				}
				return nil
			},
		},
		{
			name: "MC with lowercase answer",
			input: `Q: What manages goroutines?
A) The OS
B) The Go runtime
C) The CPU
D) The compiler
ANSWER: b
E: The Go runtime scheduler manages goroutines.`,
			qtype: TypeMultiChoice,
			checkQ: func(q *Question) error {
				if q.CorrectIdx != 1 {
					return fmt.Errorf("expected correct=1, got %d", q.CorrectIdx)
				}
				return nil
			},
		},
		{
			name: "MC with lowercase option labels",
			input: `Q: What is the result of [x**2 for x in range(3)]?
a) [0, 1, 4]
b) [1, 4, 9]
c) [0, 2, 4]
d) [1, 2, 3]
ANSWER: a
E: range(3) gives 0,1,2 and squaring gives 0,1,4.`,
			qtype: TypeMultiChoice,
			checkQ: func(q *Question) error {
				if q.CorrectIdx != 0 {
					return fmt.Errorf("expected correct=0, got %d", q.CorrectIdx)
				}
				if len(q.Options) != 4 {
					return fmt.Errorf("expected 4 options, got %d", len(q.Options))
				}
				if q.Options[0] != "[0, 1, 4]" {
					return fmt.Errorf("bad option 0: %q", q.Options[0])
				}
				return nil
			},
		},
		{
			name:    "MC missing options",
			input:   "Q: What is Docker?\nA) A container runtime\nANSWER: A",
			qtype:   TypeMultiChoice,
			wantErr: true,
		},
		{
			name: "fill-blank with multiline Q",
			input: `Q: Goroutines yield at function calls, channel ops, and ___ — not preemptive in the traditional sense.
A: runtime checkpoints
E: The Go scheduler is cooperative, not preemptive.`,
			qtype: TypeFillBlank,
			checkQ: func(q *Question) error {
				if !strings.Contains(q.Text, "___") {
					return fmt.Errorf("missing blank marker in: %q", q.Text)
				}
				return nil
			},
		},
		{
			name:  "multiline explanation",
			input: "Q: What happens if main() returns while goroutines run?\nA: All goroutines are killed immediately.\nE: When main() returns, the Go runtime tears down everything.\nNo cleanup, no waiting.\nFix: use sync.WaitGroup.",
			qtype: TypeFlashcard,
			checkQ: func(q *Question) error {
				if !strings.Contains(q.Explanation, "tears down") {
					return fmt.Errorf("missing first line in explanation: %q", q.Explanation)
				}
				if !strings.Contains(q.Explanation, "WaitGroup") {
					return fmt.Errorf("missing continuation line in explanation: %q", q.Explanation)
				}
				return nil
			},
		},
		{
			name:  "MC multiline explanation",
			input: "Q: What is the cost of creating a goroutine?\nA) ~1MB\nB) ~2KB\nC) ~64KB\nD) ~8KB\nANSWER: B\nE: Goroutines start with a tiny ~2KB stack.\nThis is 500x cheaper than OS threads.",
			qtype: TypeMultiChoice,
			checkQ: func(q *Question) error {
				if !strings.Contains(q.Explanation, "500x") {
					return fmt.Errorf("missing continuation in MC explanation: %q", q.Explanation)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := parseResponse(tt.input, tt.qtype)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkQ != nil {
				if err := tt.checkQ(q); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestParseCompareScenarioDebug(t *testing.T) {
	tests := []struct {
		name  string
		input string
		qtype QuestionType
		check func(*Question) error
	}{
		{
			name:  "compare",
			input: "Q: How do mutexes and channels differ?\nA: Mutexes protect shared memory with lock/unlock. Channels pass ownership.\nE: Think of mutexes as a lock, channels as passing a note.",
			qtype: TypeCompare,
			check: func(q *Question) error {
				if !strings.Contains(q.Text, "mutex") {
					return fmt.Errorf("missing key term in: %q", q.Text)
				}
				return nil
			},
		},
		{
			name:  "scenario",
			input: "Q: You deploy a Go service that spawns a goroutine per request but never cancels them. Traffic spikes 10x. What happens?\nA: Goroutines accumulate and OOM.\nE: Goroutine leak.",
			qtype: TypeScenario,
			check: func(q *Question) error {
				if q.Answer == "" {
					return fmt.Errorf("empty answer")
				}
				return nil
			},
		},
		{
			name:  "code-output",
			input: "Q: What does this code output?\nch := make(chan int, 2)\nch <- 1\nch <- 2\nfmt.Println(<-ch)\nA: 1\nE: Buffered channels are FIFO.",
			qtype: TypeCodeOutput,
			check: func(q *Question) error {
				if !strings.Contains(q.Text, "chan") {
					return fmt.Errorf("missing code in question: %q", q.Text)
				}
				return nil
			},
		},
		{
			name:  "debug",
			input: "Q: What is the bug in this code?\nmu.Lock()\n*counter++\nif *counter > 100 {\n    return\n}\nmu.Unlock()\nA: Early return skips Unlock, causing deadlock. Fix: defer mu.Unlock().\nE: Classic forgot-to-unlock bug.",
			qtype: TypeDebug,
			check: func(q *Question) error {
				if !strings.Contains(q.Text, "bug") {
					return fmt.Errorf("missing 'bug' keyword in: %q", q.Text)
				}
				return nil
			},
		},
		{
			name: "ordering",
			input: `Q: What is the correct order for setting up a Go module?
A) go mod init → write code → go mod tidy → go build
B) write code → go mod init → go build → go mod tidy
C) go mod init → go build → write code → go mod tidy
D) go build → go mod init → write code → go mod tidy
ANSWER: A
E: go mod init first, then code, tidy, build.`,
			qtype: TypeOrdering,
			check: func(q *Question) error {
				if q.CorrectIdx != 0 {
					return fmt.Errorf("expected correct=0, got %d", q.CorrectIdx)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := parseResponse(tt.input, tt.qtype)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err := tt.check(q); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseChallengeGrade(t *testing.T) {
	t.Run("clean challenge", func(t *testing.T) {
		input := "TITLE: Reverse a String\nLANG: Go\n---\nWrite a function that reverses a string.\nExample: \"hello\" → \"olleh\""
		ch, err := parseChallenge(input, DiffIntermediate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ch.Title != "Reverse a String" {
			t.Errorf("bad title: %q", ch.Title)
		}
		if ch.Language != "Go" {
			t.Errorf("bad language: %q", ch.Language)
		}
		if !strings.Contains(ch.Description, "reverses") {
			t.Errorf("bad description: %q", ch.Description)
		}
	})

	t.Run("challenge missing lang", func(t *testing.T) {
		input := "TITLE: FizzBuzz\n---\nWrite FizzBuzz."
		ch, err := parseChallenge(input, DiffBasic)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ch.Language != "General" {
			t.Errorf("expected General fallback, got: %q", ch.Language)
		}
	})

	t.Run("challenge missing title", func(t *testing.T) {
		_, err := parseChallenge("Just some random text", DiffBasic)
		if err == nil {
			t.Fatal("expected error for missing title")
		}
	})

	t.Run("clean grade", func(t *testing.T) {
		input := "SCORE: 85\nCORRECT: yes\nEFFICIENCY: optimal\n---\nGood solution, handles edge cases well."
		g, err := parseChallengeGrade(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if g.Score != 85 {
			t.Errorf("bad score: %d", g.Score)
		}
		if !g.Correct {
			t.Error("expected correct=true")
		}
		if g.Efficiency != "optimal" {
			t.Errorf("bad efficiency: %q", g.Efficiency)
		}
		if !strings.Contains(g.Feedback, "edge cases") {
			t.Errorf("bad feedback: %q", g.Feedback)
		}
	})

	t.Run("grade with lowercase correct", func(t *testing.T) {
		input := "SCORE: 40\nCORRECT: no\nEFFICIENCY: suboptimal\n---\nMissing edge case handling."
		g, err := parseChallengeGrade(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if g.Correct {
			t.Error("expected correct=false")
		}
	})

	t.Run("grade empty", func(t *testing.T) {
		_, err := parseChallengeGrade("")
		if err == nil {
			t.Fatal("expected error for empty grade")
		}
	})
}

func TestParseAnswerGrade(t *testing.T) {
	t.Run("correct answer", func(t *testing.T) {
		input := "CORRECT: yes\n---\nYou nailed it — goroutines are indeed lightweight threads managed by the Go runtime, not OS threads."
		g, err := parseAnswerGrade(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !g.Correct {
			t.Error("expected correct=true")
		}
		if !strings.Contains(g.Feedback, "goroutines") {
			t.Errorf("bad feedback: %q", g.Feedback)
		}
	})

	t.Run("incorrect answer", func(t *testing.T) {
		input := "CORRECT: no\n---\nYou're thinking of threads, but the concept here is different. Consider how Go schedules work units internally."
		g, err := parseAnswerGrade(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if g.Correct {
			t.Error("expected correct=false")
		}
		if !strings.Contains(g.Feedback, "threads") {
			t.Errorf("bad feedback: %q", g.Feedback)
		}
	})

	t.Run("with code fences", func(t *testing.T) {
		input := "```\nCORRECT: yes\n---\nGood understanding of closures.\n```"
		g, err := parseAnswerGrade(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !g.Correct {
			t.Error("expected correct=true")
		}
	})

	t.Run("empty input", func(t *testing.T) {
		g, err := parseAnswerGrade("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if g.Correct {
			t.Error("expected correct=false for empty")
		}
	})
}

// TestLiveChallenge tests challenge generation + grading with a real Ollama instance.
// Run with: UNROT_TEST_OLLAMA=1 go test -v -run TestLiveChallenge -count=1 ./internal/ollama/
func TestLiveChallenge(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New("")
	t.Logf("Model: %s", c.model)

	diffs := []Difficulty{DiffBasic, DiffIntermediate, DiffAdvanced}
	domains := []string{"", "Go", "Python"}

	for _, domain := range domains {
		for _, diff := range diffs {
			name := fmt.Sprintf("domain=%s/diff=%s", domain, diff)
			if domain == "" {
				name = fmt.Sprintf("domain=any/diff=%s", diff)
			}
			t.Run(name, func(t *testing.T) {
				ch, err := c.GenerateChallenge(context.Background(), domain, diff)
				if err != nil {
					t.Fatalf("GenerateChallenge failed: %v", err)
				}
				t.Logf("\nTitle: %s\nLang: %s\nDesc: %s", ch.Title, ch.Language, ch.Description)

				if ch.Title == "" {
					t.Error("FAIL: empty title")
				}
				if ch.Description == "" {
					t.Error("FAIL: empty description")
				}

				// Grade a dummy submission
				code := "// placeholder solution\nfunc solve() {}"
				grade, err := c.GradeChallenge(context.Background(), ch, code)
				if err != nil {
					t.Fatalf("GradeChallenge failed: %v", err)
				}
				t.Logf("Score: %d, Correct: %v, Efficiency: %s\nFeedback: %s",
					grade.Score, grade.Correct, grade.Efficiency, grade.Feedback)

				if grade.Feedback == "" {
					t.Error("WARN: no feedback generated")
				}
			})
		}
	}
}
