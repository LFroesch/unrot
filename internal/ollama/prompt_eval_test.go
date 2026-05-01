package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Prompt eval harness — uses local ollama to grade the quality of generated
// questions, hints, explanations, and answer grading. Run with:
//
//   UNROT_TEST_OLLAMA=1 go test -v -run TestPromptEval -count=1 -timeout=20m ./internal/ollama/
//   UNROT_MODEL=qwen2.5:14b UNROT_TEST_OLLAMA=1 go test -v -run TestPromptEval ...
//
// Each check produces a PASS/FAIL + reason. Failures don't necessarily mean
// a broken prompt — but consistent patterns across runs reveal real issues.

// evalResult captures one quality check.
type evalResult struct {
	Test    string `json:"test"`
	Type    string `json:"type"`
	Diff    string `json:"diff"`
	Topic   string `json:"topic"`
	Check   string `json:"check"`
	Pass    bool   `json:"pass"`
	Reason  string `json:"reason"`
	QText   string `json:"q_text,omitempty"`
	QAnswer string `json:"q_answer,omitempty"`
}

// judge asks ollama to evaluate a yes/no question about generated content.
// Returns (passed, reason).
func judge(c *Client, criteria, content string) (bool, string) {
	system := `You are a quality evaluator for AI-generated quiz content.
Answer with EXACTLY this format:
VERDICT: <PASS or FAIL>
REASON: <one sentence explaining why>

Be strict. If it's borderline, FAIL it.`

	resp, err := c.Chat(context.Background(), system, fmt.Sprintf("Criteria: %s\n\nContent to evaluate:\n%s", criteria, content))
	if err != nil {
		return false, fmt.Sprintf("judge error: %v", err)
	}

	pass := false
	reason := resp
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "VERDICT:"); ok {
			v := strings.TrimSpace(strings.ToUpper(after))
			pass = v == "PASS"
		}
		if after, ok := strings.CutPrefix(line, "REASON:"); ok {
			reason = strings.TrimSpace(after)
		}
	}
	return pass, reason
}

// --- Test data ---

var evalTopics = []struct {
	name    string
	content string
	file    string
}{
	{"goroutines", sampleGoroutines, "knowledge/go/goroutines.md"},
	{"docker", sampleDocker, "knowledge/docker/containers.md"},
}

// --- Core eval: question generation ---

func TestPromptEval(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New()
	t.Logf("Model: %s", c.model)

	var results []evalResult

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
		{"decision", TypeDecision},
		{"architecture", TypeArchitecture},
		{"refactor", TypeRefactor},
	}

	diffs := []Difficulty{DiffBasic, DiffIntermediate, DiffAdvanced}

	for _, topic := range evalTopics {
		for _, qt := range types {
			for _, diff := range diffs {
				name := fmt.Sprintf("%s/%s/%s", topic.name, qt.name, diff)
				t.Run(name, func(t *testing.T) {
					q, err := c.GenerateQuestion(context.Background(), topic.content, topic.file, qt.qt, diff)
					if err != nil {
						t.Fatalf("GenerateQuestion failed: %v", err)
					}

					t.Logf("Q: %s", q.Text)
					t.Logf("A: %s", q.Answer)

					qContent := fmt.Sprintf("Question: %s\nAnswer: %s\nExplanation: %s", q.Text, q.Answer, q.Explanation)
					if len(q.Options) > 0 {
						for i, opt := range q.Options {
							marker := " "
							if i == q.CorrectIdx {
								marker = "✓"
							}
							qContent += fmt.Sprintf("\n%s %c) %s", marker, 'A'+i, opt)
						}
					}

					// Check 1: Answer leak — does the question text give away the answer?
					leakPass, leakReason := judge(c,
						"Is this question well-constructed such that the answer is NOT given away in the question text? "+
							"A good question requires the student to think and recall knowledge. "+
							"FAIL if the question text contains the answer or a very close paraphrase. "+
							"FAIL if someone could guess the correct answer just by reading the question without any prior knowledge. "+
							"PASS if the question genuinely tests knowledge.",
						qContent)
					results = append(results, evalResult{
						Test: name, Type: qt.name, Diff: diff.String(), Topic: topic.name,
						Check: "no-answer-leak", Pass: leakPass, Reason: leakReason,
						QText: q.Text, QAnswer: q.Answer,
					})
					if !leakPass {
						t.Errorf("ANSWER LEAK: %s", leakReason)
					}

					// Check 2: Explanation quality — does it teach?
					explPass, explReason := judge(c,
						"Does the explanation teach WHY the answer is correct with a concrete example or mechanism? "+
							"FAIL if the explanation just restates the answer without adding insight. "+
							"FAIL if it's vague ('this is important') without concrete reasoning. "+
							"PASS if it explains the underlying concept and gives the reader something to remember.",
						fmt.Sprintf("Question: %s\nAnswer: %s\nExplanation: %s", q.Text, q.Answer, q.Explanation))
					results = append(results, evalResult{
						Test: name, Type: qt.name, Diff: diff.String(), Topic: topic.name,
						Check: "explanation-quality", Pass: explPass, Reason: explReason,
						QText: q.Text, QAnswer: q.Answer,
					})
					if !explPass {
						t.Errorf("BAD EXPLANATION: %s", explReason)
					}

					// Check 3: Difficulty match
					diffPass, diffReason := judge(c,
						fmt.Sprintf("Is this question appropriate for %s difficulty? "+
							"BASIC = core definitions, fundamental concepts. "+
							"INTERMEDIATE = application, trade-offs, comparisons. "+
							"ADVANCED = edge cases, gotchas, subtle interactions. "+
							"FAIL if the difficulty is clearly wrong (e.g. a definition question at advanced level, or an edge case at basic).", diff),
						qContent)
					results = append(results, evalResult{
						Test: name, Type: qt.name, Diff: diff.String(), Topic: topic.name,
						Check: "difficulty-match", Pass: diffPass, Reason: diffReason,
						QText: q.Text, QAnswer: q.Answer,
					})
					if !diffPass {
						t.Errorf("DIFFICULTY MISMATCH: %s", diffReason)
					}

					// Check 4: Factual accuracy (is the answer actually correct?)
					factPass, factReason := judge(c,
						"Is the stated answer factually correct based on widely accepted knowledge? "+
							"FAIL if the answer contains a factual error, an incorrect definition, or misleading information. "+
							"PASS if the answer is accurate (even if it could be more detailed).",
						fmt.Sprintf("Topic source:\n%s\n\nQuestion: %s\nStated answer: %s",
							topic.content, q.Text, q.Answer))
					results = append(results, evalResult{
						Test: name, Type: qt.name, Diff: diff.String(), Topic: topic.name,
						Check: "factual-accuracy", Pass: factPass, Reason: factReason,
						QText: q.Text, QAnswer: q.Answer,
					})
					if !factPass {
						t.Errorf("FACTUAL ERROR: %s", factReason)
					}

					// Check 5 (MC only): Plausible distractors
					if qt.qt == TypeMultiChoice || qt.qt == TypeOrdering {
						distPass, distReason := judge(c,
							"Are the wrong answer options plausible? "+
								"FAIL if any wrong option is obviously absurd or could be eliminated without knowing the topic. "+
								"FAIL if all wrong options are clearly worse than the correct one (e.g. much shorter, less detailed). "+
								"PASS if all options require actual knowledge to distinguish.",
							qContent)
						results = append(results, evalResult{
							Test: name, Type: qt.name, Diff: diff.String(), Topic: topic.name,
							Check: "plausible-distractors", Pass: distPass, Reason: distReason,
							QText: q.Text, QAnswer: q.Answer,
						})
						if !distPass {
							t.Errorf("WEAK DISTRACTORS: %s", distReason)
						}
					}
				})
			}
		}
	}

	// Write results to JSON for analysis
	writeEvalResults(t, "question_eval", results)
}

// --- Eval: answer grading (does GradeAnswer leak the correct answer?) ---

func TestPromptEvalGrading(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New()
	t.Logf("Model: %s", c.model)

	var results []evalResult

	cases := []struct {
		question      string
		correctAnswer string
		userAnswer    string
		shouldCorrect bool
		desc          string
	}{
		{
			question:      "What is a goroutine?",
			correctAnswer: "A lightweight thread managed by the Go runtime, not the OS.",
			userAnswer:    "It's a thread managed by Go's runtime, much cheaper than OS threads.",
			shouldCorrect: true,
			desc:          "correct-paraphrased",
		},
		{
			question:      "What is a goroutine?",
			correctAnswer: "A lightweight thread managed by the Go runtime, not the OS.",
			userAnswer:    "A function that runs in parallel.",
			shouldCorrect: false,
			desc:          "wrong-vague",
		},
		{
			question:      "What happens if main() returns while goroutines are running?",
			correctAnswer: "All goroutines are killed immediately.",
			userAnswer:    "The goroutines keep running in the background.",
			shouldCorrect: false,
			desc:          "wrong-opposite",
		},
		{
			question:      "What is the cost of creating a goroutine?",
			correctAnswer: "About 2KB of stack space.",
			userAnswer:    "Very small, around 2 kilobytes.",
			shouldCorrect: true,
			desc:          "correct-informal",
		},
		{
			question:      "How do you persist data across container restarts?",
			correctAnswer: "Use Docker volumes mounted with -v host:container.",
			userAnswer:    "I'm not sure, maybe save to disk?",
			shouldCorrect: false,
			desc:          "wrong-uncertain",
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			grade, err := c.GradeAnswer(context.Background(), tc.question, tc.correctAnswer, tc.userAnswer)
			if err != nil {
				t.Fatalf("GradeAnswer failed: %v", err)
			}

			t.Logf("Correct: %v, Feedback: %s", grade.Correct, grade.Feedback)

			// Check: did it grade correctly?
			if grade.Correct != tc.shouldCorrect {
				t.Errorf("WRONG GRADE: expected correct=%v, got %v", tc.shouldCorrect, grade.Correct)
			}
			results = append(results, evalResult{
				Test: tc.desc, Check: "grade-accuracy",
				Pass:   grade.Correct == tc.shouldCorrect,
				Reason: fmt.Sprintf("expected=%v got=%v", tc.shouldCorrect, grade.Correct),
			})

			// Check: does wrong-answer feedback leak the correct answer?
			if !tc.shouldCorrect && grade.Feedback != "" {
				leakPass, leakReason := judge(c,
					"Does this feedback for a WRONG answer avoid revealing the correct answer? "+
						"FAIL if the feedback states the correct answer, names it, or paraphrases it closely enough to give it away. "+
						"PASS if the feedback points the student in the right direction without giving the answer.",
					fmt.Sprintf("Correct answer (secret): %s\n\nFeedback shown to student:\n%s",
						tc.correctAnswer, grade.Feedback))
				results = append(results, evalResult{
					Test: tc.desc, Check: "grade-no-leak",
					Pass: leakPass, Reason: leakReason,
				})
				if !leakPass {
					t.Errorf("GRADE LEAKS ANSWER: %s", leakReason)
				}
			}
		})
	}

	writeEvalResults(t, "grading_eval", results)
}

// --- Eval: hint progression (do hints escalate without revealing?) ---

func TestPromptEvalHints(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New()
	t.Logf("Model: %s", c.model)

	var results []evalResult

	cases := []struct {
		question string
		answer   string
		desc     string
	}{
		{"What is the M:N scheduling model in Go?", "M goroutines multiplexed onto N OS threads by the Go scheduler.", "go-scheduler"},
		{"What is the default Docker network mode?", "Bridge mode.", "docker-network"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			var hints []string
			for i := 0; i < 3; i++ {
				// Replicate the hint logic from commands.go
				var level string
				switch {
				case i == 0:
					level = "Hint 1: Name the general TOPIC or AREA the answer relates to. One sentence."
				case i == 1:
					level = "Hint 2: Give a more specific clue — mention a KEYWORD that appears in the answer, or narrow down to the exact concept. One sentence."
				default:
					level = "Hint 3: Give a strong clue — describe the concept the answer relates to without using the exact answer words. One sentence."
				}

				system := fmt.Sprintf(`You give hints for a quiz question. The answer is provided so you can give accurate hints.
Rules:
- Output ONLY the hint text, no labels, no "Hint:", no formatting
- One sentence only
- %s`, level)

				user := fmt.Sprintf("Question: %s\nAnswer: %s", tc.question, tc.answer)
				if len(hints) > 0 {
					user += fmt.Sprintf("\n\nPrevious hints:\n%s", strings.Join(hints, "\n"))
				}

				resp, err := c.Chat(context.Background(), system, user)
				if err != nil {
					t.Fatalf("hint %d failed: %v", i+1, err)
				}
				hint := strings.TrimSpace(resp)
				hints = append(hints, hint)
				t.Logf("Hint %d: %s", i+1, hint)
			}

			// Check: hint 3 should NOT contain the exact answer
			leakPass, leakReason := judge(c,
				"Does the final hint avoid directly stating the answer? "+
					"FAIL if the hint contains the answer verbatim or makes it trivially obvious (e.g. 'the answer is X'). "+
					"PASS if the hint points toward the answer without giving it away completely.",
				fmt.Sprintf("Answer (secret): %s\n\nHint 3: %s", tc.answer, hints[2]))
			results = append(results, evalResult{
				Test: tc.desc, Check: "hint3-no-leak",
				Pass: leakPass, Reason: leakReason,
			})
			if !leakPass {
				t.Errorf("HINT 3 LEAKS: %s", leakReason)
			}

			// Check: hints should escalate (each more specific than the last)
			escPass, escReason := judge(c,
				"Do these three hints escalate from vague to specific? "+
					"Hint 1 should be the most general (topic area). "+
					"Hint 2 should narrow it down (keyword or specific concept). "+
					"Hint 3 should be the most helpful (strong directional clue). "+
					"FAIL if they're all the same specificity or if hint 1 is more specific than hint 3.",
				fmt.Sprintf("Hint 1: %s\nHint 2: %s\nHint 3: %s", hints[0], hints[1], hints[2]))
			results = append(results, evalResult{
				Test: tc.desc, Check: "hint-escalation",
				Pass: escPass, Reason: escReason,
			})
			if !escPass {
				t.Errorf("HINTS DON'T ESCALATE: %s", escReason)
			}
		})
	}

	writeEvalResults(t, "hint_eval", results)
}

// --- Eval: explanation depth ---

func TestPromptEvalExplanations(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New()
	t.Logf("Model: %s", c.model)

	var results []evalResult

	cases := []struct {
		question    string
		answer      string
		explanation string
		source      string
		desc        string
	}{
		{
			question:    "What is the cost of creating a goroutine?",
			answer:      "About 2KB of stack space.",
			explanation: "Much cheaper than OS threads.",
			source:      sampleGoroutines,
			desc:        "goroutine-cost",
		},
		{
			question:    "What command starts all Docker Compose services?",
			answer:      "docker compose up -d",
			explanation: "Starts services in detached mode.",
			source:      sampleDocker,
			desc:        "docker-compose",
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			system := `You are a tutor helping someone actually learn a concept from a quiz.
Rules:
- Start with the core concept in one plain sentence
- Then give a CONCRETE example: show real code, a real command, or real input→output
- Then explain WHY it works that way — the underlying mechanism
- Keep it to 4-6 sentences total
- Use markdown: **bold** key terms, backtick-wrapped code inline, fenced code blocks, bullet lists
- Every fact must be correct — do not make up examples`

			user := fmt.Sprintf("Question: %s\nCorrect answer: %s\nBrief explanation: %s\n\nSource material:\n%s\n\nTeach me this concept. Include a concrete example.",
				tc.question, tc.answer, tc.explanation, tc.source)

			resp, err := c.Chat(context.Background(), system, user)
			if err != nil {
				t.Fatalf("explain failed: %v", err)
			}
			t.Logf("Explanation:\n%s", resp)

			// Check: has concrete example
			exPass, exReason := judge(c,
				"Does this explanation include a CONCRETE example (code snippet, command, or input→output)? "+
					"FAIL if it's all abstract concepts with no specific example. "+
					"PASS if there's at least one concrete, runnable example.",
				resp)
			results = append(results, evalResult{
				Test: tc.desc, Check: "has-concrete-example",
				Pass: exPass, Reason: exReason,
			})
			if !exPass {
				t.Errorf("NO CONCRETE EXAMPLE: %s", exReason)
			}

			// Check: explains WHY, not just WHAT
			whyPass, whyReason := judge(c,
				"Does this explanation go beyond just restating the answer to explain WHY or HOW it works? "+
					"FAIL if it just rephrases the answer in different words. "+
					"PASS if it explains the underlying mechanism or reasoning.",
				resp)
			results = append(results, evalResult{
				Test: tc.desc, Check: "explains-why",
				Pass: whyPass, Reason: whyReason,
			})
			if !whyPass {
				t.Errorf("NO WHY: %s", whyReason)
			}
		})
	}

	writeEvalResults(t, "explanation_eval", results)
}

// --- Eval: challenge generation ---

func TestPromptEvalChallenges(t *testing.T) {
	if os.Getenv("UNROT_TEST_OLLAMA") == "" {
		t.Skip("set UNROT_TEST_OLLAMA=1 to run live Ollama tests")
	}

	c := New()
	t.Logf("Model: %s", c.model)

	var results []evalResult

	diffs := []Difficulty{DiffBasic, DiffIntermediate, DiffAdvanced}
	domains := []string{"Go", "Python"}

	for _, domain := range domains {
		for _, diff := range diffs {
			name := fmt.Sprintf("%s/%s", domain, diff)
			t.Run(name, func(t *testing.T) {
				ch, err := c.GenerateChallenge(context.Background(), domain, diff)
				if err != nil {
					t.Fatalf("GenerateChallenge failed: %v", err)
				}
				t.Logf("Title: %s\nLang: %s\nDesc: %s", ch.Title, ch.Language, ch.Description)

				// Check: solvable in reasonable scope
				scopePass, scopeReason := judge(c,
					"Is this coding challenge solvable in 5-30 lines of code? "+
						"FAIL if the problem is too vague to solve, or requires a massive implementation. "+
						"PASS if a competent developer could solve it in a focused 5-15 minute session.",
					fmt.Sprintf("Title: %s\nLanguage: %s\nDescription:\n%s", ch.Title, ch.Language, ch.Description))
				results = append(results, evalResult{
					Test: name, Check: "challenge-scope",
					Pass: scopePass, Reason: scopeReason,
				})
				if !scopePass {
					t.Errorf("BAD SCOPE: %s", scopeReason)
				}

				// Check: has concrete examples/requirements
				specPass, specReason := judge(c,
					"Does this challenge include specific requirements — concrete input/output examples, constraints, or expected behavior? "+
						"FAIL if the description is vague with no examples. "+
						"PASS if the student knows exactly what correct behavior looks like.",
					fmt.Sprintf("Description:\n%s", ch.Description))
				results = append(results, evalResult{
					Test: name, Check: "challenge-specificity",
					Pass: specPass, Reason: specReason,
				})
				if !specPass {
					t.Errorf("VAGUE CHALLENGE: %s", specReason)
				}

				// Grade a clearly wrong solution — should get low score
				badCode := "// I don't know\nfunc solve() { return nil }"
				grade, err := c.GradeChallenge(context.Background(), ch, badCode)
				if err != nil {
					t.Fatalf("GradeChallenge failed: %v", err)
				}
				t.Logf("Bad code score: %d, Feedback: %s", grade.Score, grade.Feedback)

				if grade.Score > 30 {
					t.Errorf("BAD CODE SCORED TOO HIGH: %d", grade.Score)
					results = append(results, evalResult{
						Test: name, Check: "rejects-bad-code",
						Pass: false, Reason: fmt.Sprintf("scored %d for placeholder code", grade.Score),
					})
				} else {
					results = append(results, evalResult{
						Test: name, Check: "rejects-bad-code",
						Pass: true, Reason: fmt.Sprintf("scored %d", grade.Score),
					})
				}
			})
		}
	}

	writeEvalResults(t, "challenge_eval", results)
}

// writeEvalResults dumps results as JSON for later analysis / diffing between models.
func writeEvalResults(t *testing.T, name string, results []evalResult) {
	t.Helper()

	// Summary
	total, passed, failed := len(results), 0, 0
	byCheck := map[string][2]int{} // [pass, fail]
	for _, r := range results {
		if r.Pass {
			passed++
			c := byCheck[r.Check]
			c[0]++
			byCheck[r.Check] = c
		} else {
			failed++
			c := byCheck[r.Check]
			c[1]++
			byCheck[r.Check] = c
		}
	}

	t.Logf("\n━━━ %s SUMMARY ━━━", strings.ToUpper(name))
	t.Logf("Total: %d | Pass: %d | Fail: %d (%.0f%% pass rate)", total, passed, failed,
		float64(passed)/float64(total)*100)
	for check, counts := range byCheck {
		t.Logf("  %-25s pass=%d fail=%d", check, counts[0], counts[1])
	}

	// Write JSON
	dir := os.Getenv("UNROT_EVAL_DIR")
	if dir == "" {
		dir = "."
	}
	filename := fmt.Sprintf("%s/%s_%s.json", dir, name, time.Now().Format("2006-01-02_150405"))
	data, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		t.Logf("(couldn't write results to %s: %v)", filename, err)
	} else {
		t.Logf("Results written to %s", filename)
	}
}
