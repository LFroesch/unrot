package ollama

import (
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

	c := New()
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
	}

	diffs := []Difficulty{DiffBasic, DiffIntermediate, DiffAdvanced}

	for _, tc := range cases {
		for _, qt := range types {
			for _, diff := range diffs {
				name := fmt.Sprintf("%s/%s/%s", tc.name, qt.name, diff)
				t.Run(name, func(t *testing.T) {
					q, err := c.GenerateQuestion(tc.content, tc.file, qt.qt, diff)
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
			name: "multiline explanation",
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
			name: "MC multiline explanation",
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

