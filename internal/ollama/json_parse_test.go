package ollama

import "testing"

func TestParseQuestionJSON(t *testing.T) {
	resp := `{"question":"What does Go use to multiplex many goroutines onto fewer threads?","answer":"The runtime scheduler.","explanation":"The scheduler maps goroutines onto OS threads so many lightweight tasks can share a smaller worker pool."}`

	q, err := parseQuestionJSON(resp, TypeFlashcard)
	if err != nil {
		t.Fatalf("parseQuestionJSON returned error: %v", err)
	}
	if q.Text == "" || q.Answer == "" || q.Explanation == "" {
		t.Fatalf("parseQuestionJSON returned incomplete question: %+v", q)
	}
}

func TestParseAnswerGradeJSON(t *testing.T) {
	resp := `{"correct":true,"feedback":"Your point about the scheduler mapping goroutines onto threads is the core idea."}`
	grade, err := parseAnswerGradeJSON(resp)
	if err != nil {
		t.Fatalf("parseAnswerGradeJSON returned error: %v", err)
	}
	if !grade.Correct {
		t.Fatalf("expected correct=true")
	}
	if grade.Feedback == "" {
		t.Fatalf("expected feedback")
	}
}
