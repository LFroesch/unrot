package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Messages ---

type stateLoadedMsg struct {
	state     *state.State
	files     []string
	graph     *knowledge.DepGraph
	needsPath bool // true when brain path is missing or has no files
}

type questionMsg struct {
	question *ollama.Question
	file     string
}

type learnContentMsg struct {
	content string
	domain  string
	slug    string
}

type learnSavedMsg struct {
	relPath string
}

type explainMoreMsg struct {
	text string
}

type hintMsg struct {
	text string
}

type lessonMsg struct {
	content string
	file    string
}

type conceptChatMsg struct {
	text       string
	durationMs int64
}

type learnChatMsg struct {
	text string
}

type bankNotesMsg struct{ notes string }

type reportSavedMsg struct{ path string }

type answerGradeMsg struct {
	correct  bool
	feedback string
}

type notesSavedMsg struct{ content string }

type challengeGenMsg struct {
	challenge *ollama.Challenge
}

type challengeGradeMsg struct {
	grade *ollama.ChallengeGrade
}

type auditMsg struct{ text string }
type auditFixMsg struct{ content string }

type challengeChatMsg struct{ text string }

type challengeExplainMsg struct{ text string }

type clipboardMsg struct{ ok bool }

type errMsg struct{ err error }

// --- Commands ---

func loadState(brainPath string) tea.Cmd {
	return func() tea.Msg {
		s, err := state.Load()
		if err != nil {
			return errMsg{err}
		}
		if brainPath == "" {
			return stateLoadedMsg{state: s, needsPath: true}
		}
		files, err := knowledge.Discover(brainPath)
		if err != nil {
			return stateLoadedMsg{state: s, needsPath: true}
		}
		if len(files) == 0 {
			return stateLoadedMsg{state: s, needsPath: true}
		}
		graph, _ := knowledge.BuildGraph(brainPath, files)
		return stateLoadedMsg{state: s, files: files, graph: graph}
	}
}

func generateQuestion(client *ollama.Client, brainPath, filePath string, qtype ollama.QuestionType, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return errMsg{err}
		}
		q, err := client.GenerateQuestion(content, filePath, qtype, diff)
		if err != nil {
			return errMsg{err}
		}
		return questionMsg{question: q, file: filePath}
	}
}

func explainMoreCmd(client *ollama.Client, question, answer, explanation, source string) tea.Cmd {
	return func() tea.Msg {
		system := `You are a tutor helping someone actually learn a concept from a quiz.
Rules:
- Start with the core concept in one plain sentence
- Then give a CONCRETE example: show real code, a real command, or real input→output
- Then explain WHY it works that way — the underlying mechanism
- Keep it to 4-6 sentences total
- Use markdown: **bold** key terms, backtick-wrapped code inline, fenced code blocks, bullet lists
- Every fact must be correct — do not make up examples`

		user := fmt.Sprintf("Question: %s\nCorrect answer: %s\nBrief explanation: %s", question, answer, explanation)
		if source != "" {
			user += fmt.Sprintf("\n\nSource material:\n%s", source)
		}
		user += "\n\nTeach me this concept. Include a concrete example."

		resp, err := client.Chat(system, user)
		if err != nil {
			return errMsg{err}
		}
		return explainMoreMsg{text: resp}
	}
}

func challengeExplainCmd(client *ollama.Client, challenge *ollama.Challenge, grade *ollama.ChallengeGrade, code string) tea.Cmd {
	return func() tea.Msg {
		system := `You are a tutor explaining a coding challenge.
Rules:
- Start with the core concept being tested in one sentence
- Explain WHY the right approach works — the underlying mechanism
- Mention key edge cases or gotchas the user should think about
- Give directional hints: what pattern to use, what to watch out for
- Do NOT show the full correct solution or complete code — the user should write it themselves
- You may show small snippets (1-2 lines) to illustrate a specific concept, but never the full answer
- Keep it to 6-10 sentences total
- Use markdown: **bold** key terms, fenced code blocks for snippets, bullet lists
- Every fact must be correct`

		user := fmt.Sprintf("Challenge: %s\n%s\n\nSubmitted code:\n%s", challenge.Title, challenge.Description, code)
		if grade != nil && grade.Feedback != "" {
			user += fmt.Sprintf("\n\nGrade feedback: %s", grade.Feedback)
		}
		user += "\n\nTeach me the concept and guide me toward the right approach without showing the full solution."

		resp, err := client.Chat(system, user)
		if err != nil {
			return errMsg{err}
		}
		return challengeExplainMsg{text: resp}
	}
}

// formatChatLog formats a full chat history as a copyable text log.
func formatChatLog(entries []chatEntry) string {
	var b strings.Builder
	for _, e := range entries {
		prefix := "You"
		if e.role == "assistant" {
			prefix = "AI"
		}
		b.WriteString(prefix + ": " + e.content + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

func copyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		cmds := []struct {
			name string
			args []string
		}{
			{"clip.exe", nil},
			{"xclip", []string{"-selection", "clipboard"}},
			{"xsel", []string{"--clipboard", "--input"}},
			{"wl-copy", nil},
		}
		for _, c := range cmds {
			cmd := exec.Command(c.name, c.args...)
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err == nil {
				return clipboardMsg{ok: true}
			}
		}
		return clipboardMsg{ok: false}
	}
}

func gradeAnswerCmd(client *ollama.Client, qType ollama.QuestionType, question, correctAnswer, userAnswer string) tea.Cmd {
	return func() tea.Msg {
		var grade *ollama.AnswerGrade
		var err error
		if qType == ollama.TypeFinishCode {
			grade, err = client.GradeFinishCode(question, correctAnswer, userAnswer)
		} else {
			grade, err = client.GradeAnswer(question, correctAnswer, userAnswer)
		}
		if err != nil {
			return errMsg{err}
		}
		return answerGradeMsg{correct: grade.Correct, feedback: grade.Feedback}
	}
}

func hintCmd(client *ollama.Client, question, answer string, prevHints []string) tea.Cmd {
	return func() tea.Msg {
		n := len(prevHints)
		var level string
		switch {
		case n == 0:
			level = "Hint 1: Name the general TOPIC or AREA the answer relates to. One sentence."
		case n == 1:
			level = "Hint 2: Give a more specific clue — mention a KEYWORD that appears in the answer, or narrow down to the exact concept. One sentence."
		default:
			level = "Hint 3: Give a strong clue — describe the concept the answer relates to without using the exact answer words. One sentence."
		}

		system := fmt.Sprintf(`You give hints for a quiz question. The answer is provided so you can give accurate hints.
Rules:
- Output ONLY the hint text, no labels, no "Hint:", no formatting
- One sentence only
- %s`, level)

		user := fmt.Sprintf("Question: %s\nAnswer: %s", question, answer)
		if n > 0 {
			user += fmt.Sprintf("\n\nPrevious hints:\n%s", strings.Join(prevHints, "\n"))
		}

		resp, err := client.Chat(system, user)
		if err != nil {
			return errMsg{err}
		}
		return hintMsg{text: resp}
	}
}

func learnClarifyCmd(client *ollama.Client, topic string, history []chatEntry, existingFiles []string) tea.Cmd {
	return func() tea.Msg {
		var updateNote string
		if len(existingFiles) > 0 {
			var sb strings.Builder
			sb.WriteString("\n\nExisting knowledge files in the user's library:\n")
			for _, f := range existingFiles {
				sb.WriteString("- " + f + "\n")
			}
			updateNote = sb.String()
		}

		system := fmt.Sprintf(`You are helping a developer decide what to learn and bank as a knowledge document.
Given their topic and any conversation so far, ask 1-2 clarifying questions to understand:
- Their current level with this topic (beginner/intermediate/advanced)
- Whether they want the general concept or a specific technology's implementation
- What aspects they find confusing or want to focus on

Rules:
- Keep it conversational — 2-3 sentences max
- If you already have enough context from the conversation, say: "I have a good picture. Press g to generate your knowledge doc."
- Don't repeat questions they already answered%s`, updateNote)

		msgs := make([]ollama.Message, 0, len(history)+1)
		msgs = append(msgs, ollama.Message{Role: "user", Content: fmt.Sprintf("I want to learn about: %s", topic)})
		for _, h := range history {
			msgs = append(msgs, ollama.Message{Role: h.role, Content: h.content})
		}

		resp, err := client.ChatWithHistory(system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return learnChatMsg{text: resp}
	}
}

func generateKnowledgeFromChat(client *ollama.Client, topic string, chatHistory []chatEntry, existingFiles []string, brainPath string, updateFile string) tea.Cmd {
	return func() tea.Msg {
		msgs := make([]ollama.Message, 0, len(chatHistory)+1)
		msgs = append(msgs, ollama.Message{Role: "user", Content: fmt.Sprintf("I want to learn about: %s", topic)})
		for _, h := range chatHistory {
			msgs = append(msgs, ollama.Message{Role: h.role, Content: h.content})
		}

		if updateFile != "" && brainPath != "" {
			existing, err := knowledge.ReadFile(brainPath, updateFile)
			if err != nil {
				return errMsg{err}
			}
			content, err := client.UpdateKnowledge(topic, msgs, existing)
			if err != nil {
				return errMsg{err}
			}
			domain := knowledge.Domain(updateFile)
			base := filepath.Base(updateFile)
			slug := strings.TrimSuffix(base, filepath.Ext(base))
			return learnContentMsg{content: content, domain: domain, slug: slug}
		}

		content, domain, slug, err := client.GenerateKnowledge(topic, msgs, existingFiles)
		if err != nil {
			return errMsg{err}
		}
		return learnContentMsg{content: content, domain: domain, slug: slug}
	}
}

func loadLesson(brainPath, filePath string) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return errMsg{err}
		}
		return lessonMsg{content: content, file: filePath}
	}
}

func auditKnowledgeCmd(client *ollama.Client, content, filename string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.Chat(
			`You are a knowledge file auditor for a developer's study notes. Your job is to catch things that would teach them something WRONG — not to suggest improvements or restructuring.

Check for:
- Factual errors: wrong defaults, incorrect behavior descriptions, bad code examples that wouldn't compile/run
- Misleading mental models: explanations that would lead someone to make wrong decisions in practice
- Outdated info: deprecated APIs, old syntax, practices that are no longer recommended
- Dangerous omissions: missing a critical gotcha that would bite someone in production (e.g. "goroutines are cheap" without mentioning leak risk)

Do NOT flag:
- Style, formatting, or structure preferences
- Missing topics that could be added (the doc covers what it covers)
- Suggestions to make it longer or more detailed

Output:
- If everything is factually solid: "✓ Looks good" and optionally note one strength
- If there are issues: short bullets (max 5) with the error and the correction
- Be specific — quote the wrong part and say what it should be`,
			fmt.Sprintf("File: %s\n\n%s", filename, content),
		)
		if err != nil {
			return errMsg{err}
		}
		return auditMsg{text: resp}
	}
}

func auditFixCmd(client *ollama.Client, content, auditResult string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.Chat(
			`You are fixing a knowledge file based on an audit. Apply ONLY the corrections the audit identified.

Rules:
- Fix the specific factual errors, misleading explanations, or dangerous omissions flagged in the audit
- Preserve the existing structure, sections, formatting, and ## Notes section unchanged
- Do NOT add new sections, do NOT expand content, do NOT rephrase things that weren't flagged
- Do NOT wrap the output in markdown code fences
- Output ONLY the corrected document, no commentary`,
			fmt.Sprintf("Audit findings:\n%s\n\nOriginal document:\n%s", auditResult, content),
		)
		if err != nil {
			return errMsg{err}
		}
		return auditFixMsg{content: strings.TrimSpace(resp)}
	}
}

func saveAuditFixCmd(brainPath, relPath, content string) tea.Cmd {
	return func() tea.Msg {
		absPath := filepath.Join(brainPath, relPath)
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			return errMsg{err}
		}
		// Reload to get the saved content back
		reloaded, err := knowledge.ReadFile(brainPath, relPath)
		if err != nil {
			return errMsg{err}
		}
		return notesSavedMsg{content: reloaded}
	}
}

func conceptChatCmd(client *ollama.Client, question, answer, source string, history []chatEntry) tea.Cmd {
	return func() tea.Msg {
		var system string
		if question != "" {
			system = fmt.Sprintf(`You are a tutor helping someone understand a concept they were quizzed on.

Quiz question: %s
Correct answer: %s`, question, answer)
		} else {
			system = `You are a tutor helping someone understand a concept they are studying.`
		}
		if source != "" {
			system += fmt.Sprintf("\n\nSource material:\n%s", source)
		}
		system += `

Be a helpful tutor:
- Follow the user's lead — if they ask for a different format, tone, or to skip examples, adapt immediately and keep that change for the rest of the conversation
- Never repeat examples or code blocks from your earlier messages; reference them by description if needed
- When the user seems confused, try a completely different angle: analogy, step-by-step trace, or compare/contrast
- Use markdown when it aids clarity
- Be accurate — don't make things up`

		msgs := make([]ollama.Message, len(history))
		for i, h := range history {
			msgs[i] = ollama.Message{Role: h.role, Content: h.content}
		}
		start := time.Now()
		resp, err := client.ChatWithHistory(system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return conceptChatMsg{text: resp, durationMs: time.Since(start).Milliseconds()}
	}
}

func saveKnowledge(brainPath, domain, slug, content string) tea.Cmd {
	return func() tea.Msg {
		relPath, err := knowledge.WriteFile(brainPath, domain, slug, content)
		if err != nil {
			return errMsg{err}
		}
		return learnSavedMsg{relPath: relPath}
	}
}

func bankChatToNotesCmd(client *ollama.Client, chat []chatEntry, brainPath, relPath, existingContent string) tea.Cmd {
	return func() tea.Msg {
		var sb strings.Builder
		for _, msg := range chat {
			if msg.role == "user" {
				sb.WriteString("User: " + msg.content + "\n")
			} else {
				sb.WriteString("Assistant: " + msg.content + "\n")
			}
		}

		system := `You are summarizing a tutoring conversation into concise study notes.
Rules:
- Extract the key insights, "aha moments", and important facts from the conversation
- Output bullet points (- prefix), 3-8 bullets max
- Be concise — each bullet should be one line
- Focus on what the student learned, not the questions they asked
- Don't include greetings or meta-conversation`

		user := fmt.Sprintf("Conversation:\n%s\n\nSummarize the key insights as study notes.", sb.String())
		resp, err := client.Chat(system, user)
		if err != nil {
			return errMsg{err}
		}
		return bankNotesMsg{notes: resp}
	}
}

func saveNotesCmd(brainPath, relPath, notes string) tea.Cmd {
	return func() tea.Msg {
		if err := knowledge.UpdateNotes(brainPath, relPath, notes); err != nil {
			return errMsg{err}
		}
		content, err := knowledge.ReadFile(brainPath, relPath)
		if err != nil {
			return errMsg{err}
		}
		return notesSavedMsg{content: content}
	}
}

func (m model) exportReportCmd() tea.Cmd {
	correct := m.sessionCorrect
	wrong := m.sessionWrong
	total := m.sessionTotal
	confSum := m.sessionConfSum
	wrongs := m.sessionWrongs
	domains := m.sessionDomains
	start := m.sessionStart

	return func() tea.Msg {
		var b strings.Builder
		b.WriteString("# Unrot Session Report\n\n")
		b.WriteString(fmt.Sprintf("**Date:** %s\n", time.Now().Format("2006-01-02 15:04")))
		b.WriteString(fmt.Sprintf("**Duration:** %s\n", time.Since(start).Round(time.Second)))
		b.WriteString(fmt.Sprintf("**Questions:** %d\n", total))
		if total > 0 {
			pct := correct * 100 / total
			avg := float64(confSum) / float64(total)
			b.WriteString(fmt.Sprintf("**Score:** %d%% (%d correct, %d wrong)\n", pct, correct, wrong))
			b.WriteString(fmt.Sprintf("**Avg Confidence:** %.1f\n", avg))
		}

		var domainList []string
		for d := range domains {
			domainList = append(domainList, d)
		}
		if len(domainList) > 0 {
			b.WriteString(fmt.Sprintf("**Domains:** %s\n", strings.Join(domainList, ", ")))
		}

		if len(wrongs) > 0 {
			b.WriteString("\n## Review These\n\n")
			for _, w := range wrongs {
				b.WriteString(fmt.Sprintf("### %s (%s)\n\n", w.file, w.qtype))
				b.WriteString(fmt.Sprintf("**Q:** %s\n\n", w.question))
				b.WriteString(fmt.Sprintf("**A:** %s\n\n", w.answer))
			}
		}

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".local", "share", "unrot", "reports")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return errMsg{err}
		}
		filename := fmt.Sprintf("session-%s.md", time.Now().Format("2006-01-02-150405"))
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
			return errMsg{err}
		}

		return reportSavedMsg{path: path}
	}
}

func challengeClarifyCmd(client *ollama.Client, topic string, history []chatEntry) tea.Cmd {
	return func() tea.Msg {
		system := `You are helping a developer choose what coding challenge to practice.
Given their topic and any conversation so far, ask 1-2 clarifying questions to understand:
- Their current level (beginner/intermediate/advanced)
- What specific area they want to drill (syntax, algorithms, debugging, etc.)
- Any language preference

Rules:
- Keep it conversational — 2-3 sentences max
- If you already have enough context, say: "Got it. Press g to generate your challenge."
- Don't repeat questions they already answered`

		msgs := make([]ollama.Message, 0, len(history)+1)
		msgs = append(msgs, ollama.Message{Role: "user", Content: fmt.Sprintf("I want to practice: %s", topic)})
		for _, h := range history {
			msgs = append(msgs, ollama.Message{Role: h.role, Content: h.content})
		}

		resp, err := client.ChatWithHistory(system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return challengeChatMsg{text: resp}
	}
}

func generateChallengeFromChatCmd(client *ollama.Client, topic string, history []chatEntry, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		msgs := make([]ollama.Message, len(history))
		for i, h := range history {
			msgs[i] = ollama.Message{Role: h.role, Content: h.content}
		}
		ch, err := client.GenerateChallengeFromChat(topic, msgs, diff)
		if err != nil {
			return errMsg{err}
		}
		return challengeGenMsg{challenge: ch}
	}
}

func generateChallengeCmd(client *ollama.Client, domain string, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		ch, err := client.GenerateChallenge(domain, diff)
		if err != nil {
			return errMsg{err}
		}
		return challengeGenMsg{challenge: ch}
	}
}

func gradeChallengeCmd(client *ollama.Client, challenge *ollama.Challenge, code string) tea.Cmd {
	return func() tea.Msg {
		grade, err := client.GradeChallenge(challenge, code)
		if err != nil {
			return errMsg{err}
		}
		return challengeGradeMsg{grade: grade}
	}
}
