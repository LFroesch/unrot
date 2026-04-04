package main

import (
	"context"
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
	durationSec float64
}

type learnChatMsg struct {
	text string
}

type bankNotesMsg struct{ notes string }

// enrichDoneMsg fires after a single file has been enriched.
type enrichDoneMsg struct {
	relPath string
	err     error
}

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

type clipboardMsg struct{ ok bool }

type projectSubsystemsMsg struct{ subsystems []string }
type projectContentMsg struct{ content string }
type projectFilesMsg struct{ files []string }
type projectFileProcessedMsg struct {
	notes   string
	fileIdx int
}

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

func generateQuestion(ctx context.Context, client *ollama.Client, brainPath, filePath string, qtype ollama.QuestionType, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return errMsg{err}
		}
		q, err := client.GenerateQuestion(ctx, content, filePath, qtype, diff)
		if err != nil {
			return errMsg{err}
		}
		return questionMsg{question: q, file: filePath}
	}
}

func explainMoreCmd(ctx context.Context, client *ollama.Client, question, answer, explanation, source string) tea.Cmd {
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

		resp, err := client.Chat(ctx, system, user)
		if err != nil {
			return errMsg{err}
		}
		return explainMoreMsg{text: resp}
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

func gradeAnswerCmd(ctx context.Context, client *ollama.Client, qType ollama.QuestionType, question, correctAnswer, userAnswer string) tea.Cmd {
	return func() tea.Msg {
		// Short-circuit exact match — LLM should never fail this.
		if strings.EqualFold(strings.TrimSpace(userAnswer), strings.TrimSpace(correctAnswer)) {
			return answerGradeMsg{correct: true, feedback: "Correct!"}
		}
		var grade *ollama.AnswerGrade
		var err error
		if qType == ollama.TypeFinishCode {
			grade, err = client.GradeFinishCode(ctx, question, correctAnswer, userAnswer)
		} else {
			grade, err = client.GradeAnswer(ctx, question, correctAnswer, userAnswer)
		}
		if err != nil {
			return errMsg{err}
		}
		return answerGradeMsg{correct: grade.Correct, feedback: grade.Feedback}
	}
}

func hintCmd(ctx context.Context, client *ollama.Client, question, answer string, prevHints []string) tea.Cmd {
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

		resp, err := client.Chat(ctx, system, user)
		if err != nil {
			return errMsg{err}
		}
		return hintMsg{text: resp}
	}
}

func challengeHintCmd(ctx context.Context, client *ollama.Client, problem string, prevHints []string) tea.Cmd {
	return func() tea.Msg {
		n := len(prevHints)
		var level string
		switch {
		case n == 0:
			level = "Hint 1: Name the general technique or approach needed. One sentence."
		case n == 1:
			level = "Hint 2: Mention a specific function, method, or language feature that would help. One sentence."
		default:
			level = "Hint 3: Describe the key step or algorithm without giving the full solution. One sentence."
		}

		system := fmt.Sprintf(`You give hints for a coding challenge. Do NOT reveal the solution.
Rules:
- Output ONLY the hint text, no labels, no "Hint:", no formatting
- One sentence only
- %s`, level)

		user := fmt.Sprintf("Challenge: %s", problem)
		if n > 0 {
			user += fmt.Sprintf("\n\nPrevious hints:\n%s", strings.Join(prevHints, "\n"))
		}

		resp, err := client.Chat(ctx, system, user)
		if err != nil {
			return errMsg{err}
		}
		return hintMsg{text: resp}
	}
}

func learnClarifyCmd(ctx context.Context, client *ollama.Client, topic string, history []chatEntry, existingFiles []string) tea.Cmd {
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
- If you already have enough context from the conversation, say: "I have a good picture. Press ctrl+g to generate your knowledge doc."
- Don't repeat questions they already answered%s`, updateNote)

		msgs := make([]ollama.Message, 0, len(history)+1)
		msgs = append(msgs, ollama.Message{Role: "user", Content: fmt.Sprintf("I want to learn about: %s", topic)})
		for _, h := range history {
			msgs = append(msgs, ollama.Message{Role: h.role, Content: h.content})
		}

		resp, err := client.ChatWithHistory(ctx, system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return learnChatMsg{text: resp}
	}
}

func generateKnowledgeFromChat(ctx context.Context, client *ollama.Client, topic string, chatHistory []chatEntry, existingFiles []string, brainPath string, updateFile string) tea.Cmd {
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
			content, err := client.UpdateKnowledge(ctx, topic, msgs, existing)
			if err != nil {
				return errMsg{err}
			}
			domain := knowledge.Domain(updateFile)
			base := filepath.Base(updateFile)
			slug := strings.TrimSuffix(base, filepath.Ext(base))
			return learnContentMsg{content: content, domain: domain, slug: slug}
		}

		content, domain, slug, err := client.GenerateKnowledge(ctx, topic, msgs, existingFiles)
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

func auditKnowledgeCmd(ctx context.Context, client *ollama.Client, content, filename string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.Chat(ctx,
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

func auditFixCmd(ctx context.Context, client *ollama.Client, content, auditResult string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.Chat(ctx,
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

func conceptChatCmd(ctx context.Context, client *ollama.Client, question, answer, source string, history []chatEntry) tea.Cmd {
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
- NEVER give away the full answer. You can give code examples, but using different data, or not the full answer.
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
		resp, err := client.ChatWithHistory(ctx, system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return conceptChatMsg{text: resp, durationSec: time.Since(start).Seconds()}
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

func bankChatToNotesCmd(ctx context.Context, client *ollama.Client, chat []chatEntry, brainPath, relPath, existingContent string) tea.Cmd {
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
		resp, err := client.Chat(ctx, system, user)
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

func challengeClarifyCmd(ctx context.Context, client *ollama.Client, topic string, history []chatEntry) tea.Cmd {
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

		resp, err := client.ChatWithHistory(ctx, system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return challengeChatMsg{text: resp}
	}
}

func generateChallengeFromChatCmd(ctx context.Context, client *ollama.Client, topic string, history []chatEntry, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		msgs := make([]ollama.Message, len(history))
		for i, h := range history {
			msgs[i] = ollama.Message{Role: h.role, Content: h.content}
		}
		ch, err := client.GenerateChallengeFromChat(ctx, topic, msgs, diff)
		if err != nil {
			return errMsg{err}
		}
		return challengeGenMsg{challenge: ch}
	}
}

func generateChallengeCmd(ctx context.Context, client *ollama.Client, domain string, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		ch, err := client.GenerateChallenge(ctx, domain, diff)
		if err != nil {
			return errMsg{err}
		}
		return challengeGenMsg{challenge: ch}
	}
}

func gradeChallengeCmd(ctx context.Context, client *ollama.Client, challenge *ollama.Challenge, code string) tea.Cmd {
	return func() tea.Msg {
		grade, err := client.GradeChallenge(ctx, challenge, code)
		if err != nil {
			return errMsg{err}
		}
		return challengeGradeMsg{grade: grade}
	}
}

// enrichFileCmd reads a knowledge file, calls ollama to tag difficulty + connections,
// then writes the result back to the file's ## Connections section.
func enrichFileCmd(ctx context.Context, client *ollama.Client, brainPath, indexContent, relPath string) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, relPath)
		if err != nil {
			return enrichDoneMsg{relPath: relPath, err: err}
		}
		dom := knowledge.Domain(relPath)
		// slug = filename without extension
		slug := strings.TrimSuffix(filepath.Base(relPath), ".md")
		difficulty, connections, err := client.EnrichFile(ctx, content, indexContent, dom, slug)
		if err != nil {
			return enrichDoneMsg{relPath: relPath, err: err}
		}
		err = knowledge.UpdateConnections(brainPath, relPath, difficulty, connections)
		return enrichDoneMsg{relPath: relPath, err: err}
	}
}

// --- Project Scan Commands ---

func proposeSubsystemsCmd(ctx context.Context, client *ollama.Client, archContext, fileTree string) tea.Cmd {
	return func() tea.Msg {
		projectLog("proposing subsystems...")
		subs, err := client.ProposeSubsystems(ctx, archContext, fileTree)
		if err != nil {
			return errMsg{err}
		}
		projectLog("proposed %d subsystems: %v", len(subs), subs)
		return projectSubsystemsMsg{subsystems: subs}
	}
}

// suggestFilesCmd asks ollama which source files to read for a subsystem.
func suggestFilesCmd(ctx context.Context, client *ollama.Client, repoPath, archContext, subsystem string, chatHistory []chatEntry) tea.Cmd {
	return func() tea.Msg {
		projectLog("[%s] asking ollama to pick files...", subsystem)
		tree := listRepoTree(repoPath)
		msgs := make([]ollama.Message, len(chatHistory))
		for i, h := range chatHistory {
			msgs[i] = ollama.Message{Role: h.role, Content: h.content}
		}
		files, err := client.SuggestFiles(ctx, tree, archContext, subsystem, msgs)
		if err != nil {
			return errMsg{err}
		}

		// Build a set of all real files for fuzzy matching
		treeFiles := strings.Split(tree, "\n")
		byBasename := make(map[string][]string)
		for _, tf := range treeFiles {
			tf = strings.TrimSpace(tf)
			if tf != "" {
				byBasename[filepath.Base(tf)] = append(byBasename[filepath.Base(tf)], tf)
			}
		}

		// Validate files exist, with fallback strategies
		seen := make(map[string]bool)
		var valid []string
		addIfExists := func(f string) bool {
			if seen[f] {
				return false
			}
			abs := filepath.Join(repoPath, f)
			if _, err := os.Stat(abs); err == nil {
				seen[f] = true
				valid = append(valid, f)
				return true
			}
			return false
		}
		for _, f := range files {
			if addIfExists(f) {
				continue
			}
			// Try stripping a leading directory (ollama often prepends repo name)
			if idx := strings.IndexByte(f, '/'); idx >= 0 {
				if addIfExists(f[idx+1:]) {
					continue
				}
			}
			// Basename fallback — if unique match in tree
			base := filepath.Base(f)
			if matches, ok := byBasename[base]; ok && len(matches) == 1 {
				addIfExists(matches[0])
			}
		}
		if len(valid) == 0 {
			projectLog("[%s] no valid files found, skipping", subsystem)
			return errMsg{fmt.Errorf("no source files found for %s subsystem", subsystem)}
		}
		// Cap at 3 files per subsystem to keep ollama calls manageable on small models
		if len(valid) > 3 {
			valid = valid[:3]
		}
		projectLog("[%s] selected %d files: %v", subsystem, len(valid), valid)
		return projectFilesMsg{files: valid}
	}
}

// processFileCmd reads a single source file and extracts notes, chaining with accumulated context.
func processFileCmd(ctx context.Context, client *ollama.Client, repoPath string, files []string, idx int, runningNotes, subsystem string) tea.Cmd {
	return func() tea.Msg {
		f := files[idx]
		projectLog("[%s] reading %s (%d/%d)...", subsystem, f, idx+1, len(files))
		data, err := os.ReadFile(filepath.Join(repoPath, f))
		if err != nil {
			projectLog("[%s] ERROR reading %s: %v", subsystem, f, err)
			// Skip unreadable files, pass notes through
			return projectFileProcessedMsg{notes: runningNotes, fileIdx: idx}
		}

		content := string(data)
		lines := strings.Split(content, "\n")

		// Cap very large files to avoid excessive ollama calls
		const maxLines = 1500
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			content = strings.Join(lines, "\n")
		}

		// If file is large, chunk it — process in ~600 line chunks, accumulating notes per chunk
		const chunkSize = 600
		if len(lines) > chunkSize {
			chunkNotes := runningNotes
			for start := 0; start < len(lines); start += chunkSize {
				end := start + chunkSize
				if end > len(lines) {
					end = len(lines)
				}
				chunkLabel := fmt.Sprintf("%s (lines %d-%d)", f, start+1, end)
				chunk := strings.Join(lines[start:end], "\n")
				extracted, err := client.ExtractFileNotes(ctx, chunkLabel, chunk, chunkNotes, subsystem)
				if err != nil {
					break
				}
				chunkNotes = extracted
			}
			return projectFileProcessedMsg{notes: chunkNotes, fileIdx: idx}
		}

		notes, err := client.ExtractFileNotes(ctx, f, content, runningNotes, subsystem)
		if err != nil {
			return projectFileProcessedMsg{notes: runningNotes, fileIdx: idx}
		}
		return projectFileProcessedMsg{notes: notes, fileIdx: idx}
	}
}

// generateProjectFromNotesCmd synthesizes a knowledge doc from accumulated file notes.
func generateProjectFromNotesCmd(ctx context.Context, client *ollama.Client, projectName, subsystem, archContext, notes string, chatHistory []chatEntry) tea.Cmd {
	return func() tea.Msg {
		projectLog("[%s] synthesizing knowledge doc from notes (%d chars)...", subsystem, len(notes))
		msgs := make([]ollama.Message, len(chatHistory))
		for i, h := range chatHistory {
			msgs[i] = ollama.Message{Role: h.role, Content: h.content}
		}
		content, err := client.GenerateProjectFromNotes(ctx, projectName, subsystem, archContext, notes, msgs)
		if err != nil {
			projectLog("[%s] ERROR synthesizing: %v", subsystem, err)
			return errMsg{err}
		}
		projectLog("[%s] doc generated (%d chars)", subsystem, len(content))
		return projectContentMsg{content: content}
	}
}

// listRepoTree returns a compact file tree for the repo (source files only).
func listRepoTree(repoPath string) string {
	sourceExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
		".rs": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".rb": true,
		".sh": true, ".sql": true, ".yaml": true, ".yml": true, ".toml": true,
		".md": true, ".json": true, ".proto": true, ".graphql": true,
	}
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
		"dist": true, "build": true, ".next": true, "target": true,
	}

	var files []string
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if sourceExts[filepath.Ext(info.Name())] {
			rel, _ := filepath.Rel(repoPath, path)
			files = append(files, rel)
		}
		return nil
	})

	// Cap at 200 files to keep context small
	if len(files) > 200 {
		files = files[:200]
	}
	return strings.Join(files, "\n")
}

// readProjectContext reads CLAUDE.md and README.md from a repo directory.
func readProjectContext(repoPath string) string {
	var parts []string
	for _, name := range []string{"CLAUDE.md", "README.md"} {
		data, err := os.ReadFile(filepath.Join(repoPath, name))
		if err == nil {
			parts = append(parts, fmt.Sprintf("--- %s ---\n%s", name, string(data)))
		}
	}
	return strings.Join(parts, "\n\n")
}

// getRepoHead returns the short HEAD commit hash for a git repo, or "" on error.
func getRepoHead(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// projectLog appends a timestamped line to ~/.local/share/unrot/project-batch.log.
func projectLog(format string, args ...any) {
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".local", "share", "unrot", "project-batch.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
}

// batchSaveKnowledgeCmd saves a project subsystem doc with source metadata attached.
func batchSaveKnowledgeCmd(brainPath, repoPath, projectName, subsystem, content, sourceFiles string) tea.Cmd {
	return func() tea.Msg {
		files := sourceFiles
		if files == "" {
			files = "(architecture context only)"
		}
		sourceMeta := &knowledge.SourceMeta{
			Repo:     repoPath,
			Files:    files,
			Analyzed: time.Now().Format("2006-01-02"),
			Commit:   getRepoHead(repoPath),
		}
		full := content + "\n\n" + knowledge.FormatSource(sourceMeta)
		domain := "projects/" + projectName
		projectLog("saving %s/%s (files: %s)", domain, subsystem, files)
		relPath, err := knowledge.WriteFile(brainPath, domain, subsystem, full)
		if err != nil {
			projectLog("ERROR saving %s/%s: %v", domain, subsystem, err)
			return errMsg{err}
		}
		projectLog("saved -> %s", relPath)
		return learnSavedMsg{relPath: relPath}
	}
}

// getRepoCommitDrift returns how many commits ahead the repo HEAD is from a stored commit.
// Returns -1 if comparison fails.
func getRepoCommitDrift(repoPath, storedCommit string) int {
	if storedCommit == "" || repoPath == "" {
		return -1
	}
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--count", storedCommit+"..HEAD")
	out, err := cmd.Output()
	if err != nil {
		return -1
	}
	n := strings.TrimSpace(string(out))
	var count int
	fmt.Sscanf(n, "%d", &count)
	return count
}
