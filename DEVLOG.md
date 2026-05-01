## DevLog

### 2026-04-30: Onboarding fixes for new users
- README: added "Step 0" Ollama install + `ollama pull qwen2.5:7b` block; corrected `UNROT_MODEL` default from `qwen2.5:3b` → `qwen2.5:7b` to match `ollama.New()`
- `--brain <path>` CLI flag overrides `SECOND_BRAIN` env and saved state for one-shot use
- Settings toast on missing knowledge path now explains *why* (e.g. "no .md files found under <path>/knowledge/<domain>/") instead of generic "set your knowledge path"
- Remaining onboarding gaps (Ollama liveness check, "Second Brain" jargon, default question-type subset, JSON parsing) noted at top of WORK.md
- Files: `README.md`, `main.go`, `commands.go`, `update.go`, `WORK.md`

### 2026-04-30: Project mode — drop late staleness results after cancel
Cancelling the staleness check (`esc`/`q` during projectCheckingStale) returned the user to repo input, but the in-flight `checkProjectStalenessCmd` would still deliver a `projectStaleCheckMsg` later and snap the UI into projectStaleResult/Proposing — losing the cancel. Guarded the message handler to ignore results unless the user is still in projectCheckingStale. Files: update.go.

### 2026-04-30: Remove hardcoded absolute user path from test fixture
- Replaced the `"/Users/example/..."` settings-wrap regression fixture with a `filepath.Join(...)` path so the test stays platform-neutral
- No runtime path logic changed; the app still resolves user directories via `os.UserHomeDir` and `filepath.Join`
- `helpers_test.go`, `WORK.md`

### 2026-04-27: Topic list full-width cursor bar + expanded help
- Selected row in browse-topics now pads to full terminal width with bg color (was capped at ~52 cols)
- Help screen (`?`) reorganized into grouped sections: Global, Dashboard, Topic list, Quiz question, Quiz result, Chat/Knowledge, Learn/Project/Challenge
- Added previously undocumented keys to help: `ctrl+b/y/l`, `ctrl+s`, `1-5`, `a/b/c/d`, `ctrl+g/r/e`, `f/x/+/w/r/F/R/i/I/p/v`
- `view.go:renderTopicList`, `view.go:renderHelp`

### 2026-04-27: `q` as universal back/cancel alias for `esc`
- `q` now mirrors `esc` everywhere a text input isn't focused: recent zone, topic list, lesson, quiz loading/grading, knowledge tab, MC question, result, knowledge/domain overlays, learn generating/review, viewer, settings, stats, challenge loading/grading, challenge problem tab, challenge result (non-chat), project sub-states, error
- Skipped (textarea active): chat overlay, notes overlay, answer textarea on typed questions, learn input/chat, challenge code/chat tabs, challenge input/chat, project repo input, settings brain-path edit
- Dashboard `q` retained as quit (no "back" target from home); `ctrl+c` still force-quits
- Removed redundant `case "q"` in lesson handler now that `esc, q` share the skipToNextFile path
- `update.go`, `CLAUDE.md`

### 2026-04-18: Topic list — tighter rows, cursor bar, centered suffix column
- Removed blank-line gap between domain groups and the extra spacer line above the list
- Added a row background highlight on the selected file (color 237) so the cursor is visually obvious
- Capped the name column to 28 chars so the confidence dots + "Xd ago" suffix sits roughly in the middle of the row instead of pinned to the right edge
- Narrow-terminal fallback (ww < 60) keeps the old proportional layout
- `view.go:renderTopicList`

### 2026-04-18: Softer grading, feedback grounded in user's wording
- Reworked `GradeAnswer` and `GradeFinishCode` system prompts in `internal/ollama/ollama.go`
- Added explicit grading philosophy ("learning tool, not an exam/compiler — lean toward CORRECT")
- Feedback must reference the user's actual wording/code (quote or paraphrase their phrasing) so it reads as a response to what they said, not a generic rubric
- Tightened WRONG criteria: only for true semantic errors / missed core concept, not style or incompleteness

### 2026-04-18: UI sweep, width regressions, docs cleanup
- Tightened width handling for the header bar so left/right metadata no longer drift or overflow as easily on narrower terminals
- Reworked topic list rows to use stable name/suffix columns and improved stats domain table alignment
- Wrapped long settings values and descriptions (knowledge path, debug log path, enrichment text) so settings stays readable without assuming a wide terminal
- Added regression tests for width-sensitive helper/render behavior in the main package
- Fixed README inaccuracies: removed `make install`, corrected the default session wording, documented project scan/recent flows, and expanded the settings description
- Naming decision for this pass: keep `unrot` for now; no binary/path rename yet
- Key files: `helpers.go`, `view.go`, `update.go`, `helpers_test.go`, `README.md`, `WORK.md`

### 2026-04-05: v60 — Interview mode, difficulty gating fix, project prompt fixes
- `I` on dashboard: interview mode — reviews all `projects/` files with decision/architecture/refactor types only
- `savedActiveTypes` restores user's quiz type config on `goHome()`
- Fixed `applyDifficultyGating`: domains with no "easy" tier no longer lock all medium files (was permanently deferring all project docs)
- Added `- difficulty: medium` to both project gen prompts so files have correct metadata
- Clarified `requires:` prompt — omit if none (prevents spurious requires lines)

### 2026-04-04: v59 — Project mode overhaul: two-pass generation, staleness, logging
- Two-pass generation: `ExtractFileNotes` per file → `GenerateProjectFromNotes` synthesis (was single massive prompt)
- Smaller prompts per call = faster on qwen2.5:7b, better quality from focused per-file analysis
- Bumped subsystem count from 3-5 to 5-8, file count from 1-4 to 1-6 per subsystem
- Staleness checking: reads existing `## Source` commit hash, shows drift, offers stale-only or full re-scan
- New sub-states: `projectCheckingStale`, `projectStaleResult` between repoInput and proposing
- Ollama request/response logging wired up for project mode (single log file per scan session)
- Batch entry statuses: "extracting" → "synthesizing" → "saving" → "done"
- Progress view shows file-level extraction progress per subsystem

### 2026-04-04: v58 — Project mode overhaul: frictionless one-shot generation
- Completely rethought project mode for efficiency on small models (qwen2.5:7b)
- Flow: enter repo path → 1 ollama call proposes subsystems WITH file mappings → 1 call per subsystem generates from actual source → done
- Total ~4-6 ollama calls for a whole project (was 25-40+ with old extract-then-synthesize pipeline)
- `ProposeSubsystems` returns `SubsystemProposal` structs (slug + desc + files) — no separate SuggestFiles call
- `generateSubsystemCmd` reads source files directly from disk, smart truncation (800 lines/file, 2000 total), ONE generation call
- Interview-focused prompts: "Architecture & Patterns" + "Interview Angles" sections in generated docs
- `GenerateProjectKnowledge` prompt rewritten for interview prep focus
- Removed entire multi-call pipeline: `suggestFilesCmd`, `processFileCmd`, `ExtractFileNotes` chain, `generateProjectFromNotesCmd`
- Removed steps: `projectPicking`, `projectChat`, `projectScanning`, `projectProcessing`, `projectReview`
- Only 4 steps remain: repoInput → proposing → generating → done
- Progress view with spinner, checkmarks, elapsed timer

### 2026-04-03: v57 — Project mode POC
- `p` on dashboard enters project scan flow: repo path → subsystem proposal → chat → generate → review → save
- `Domain()` updated to handle two-level `projects/<name>` domains (special case for `projects/` prefix)
- `SourceMeta` struct + `ParseSource()`/`FormatSource()` in knowledge package for `## Source` metadata
- 3 new question types: `decision` (justify design choices), `architecture` (trace data flows), `refactor` (propose improvements)
- `ProposeSubsystems()` and `GenerateProjectKnowledge()` in ollama.go
- `phaseProject` with 6 sub-states: repoInput → proposing → picking → chat → generating → review
- Saves to `knowledge/projects/<name>/<subsystem>.md` with auto-populated `## Source` metadata (repo, commit, date)
- Phase 2 TODO: read actual source files for subsystem (currently uses arch context as proxy)

### 2026-04-03: v56 — Polish: reset confirm, settings scroll, timing
- `x` in topic list now requires confirmation (press x twice, n/esc to cancel)
- Settings page scrolls viewport to keep cursor visible when navigating with j/k
- Chat response timing changed from milliseconds to seconds (e.g. "2.3s" instead of "2300ms")

### 2026-04-03: v55 — Recent zone
- `R` on dashboard opens recent zone (last 10 answered questions)
- `RecentQuestion` struct added to state.json — persists file, type, question text, correct/wrong, timestamp
- j/k to scroll, enter to retry that file (single-file quiz), esc to go back
- Grade icon (✓/✗), age label, domain/slug/type metadata per entry
- `AddRecentQuestion` called in both grading paths (confidence rating + auto-advance on esc)

### 2026-04-03: v54 — New achievements, casino popups, session toasts
- 6 new achievements: Code Monkey (first challenge), Grinder (10 challenges), Flawless Code (score 100), Fact Checker (audit fix), Note Hoarder (5 notes banked), Iron Mind (50 questions/session)
- `TotalChallenges` + `NotesBanked` added to State/stateJSON
- Casino tier system (`casinoTier`): lucky (🎰) now also shows popup, not just jackpot (💎)
- Session milestone toasts at 10/25/50/100 questions
- Daily goal hit toast
- Achievement checks wired into challenge grading, bank notes, and audit fix handlers

### 2026-04-03: v53 — Challenge UX overhaul: quiz-like flow, retry, better grading
- Removed entire tab system (cTabChat/cTabProblem/cTabCode/cTabExplanation) — ~200 lines deleted
- Challenge now mirrors quiz: problem viewport (top) + code textarea (bottom) during working; inline result with submitted code + score + feedback
- Added retry: `r` on result resets to working with same challenge; `challengeRetrying bool` skips XP re-award on re-grade
- Improved `GradeChallenge` prompt: dimension-based scoring (correctness 50/efficiency 30/quality 20), specific edge case callouts, directional hints without solution reveal
- Removed `challengeExplain`, `challengeExplainCmd`, `syncChallengeTab`, `syncChallengeChatViewport` — no longer needed

### 2026-04-03: v52 — Challenge polish: chat logging, anti-leak grading, result UX
- Pre-question chat history seeded into `conceptChat` on challenge gen — chat tab now shows the clarification conversation during working/result
- `GradeChallenge` prompt: added CRITICAL anti-leak rule — feedback gives directional hints only, never the full solution
- `challengeExplainCmd` prompt: same anti-leak rule — shows concept + small snippets, never the full correct implementation
- Result page: added inline `e → deeper explanation · enter → next challenge` hint when explanation not yet loaded

### 2026-04-03: v51 — Bubbleup notifications, header tabs, difficulty fix
- Integrated `go.dalton.dog/bubbleup` for auto-dismissing overlay notifications (level-ups, achievements, clipboard, favorites)
- Custom alert types: `LevelUp` (yellow ✦), `Achievement` (yellow ★), `XP` (green), `Hint` (blue)
- Legacy `m.toast` kept for inline action hints (audit prompts, bank review); bubbleup handles timed notifications
- Moved question tabs (chat/quiz/knowledge) and challenge tabs into header bar with matching `#235` background
- Removed duplicate tab bars from content area, adjusted `contentHeight()` for extra header line
- Fixed difficulty always showing in header — `DiffBasic` (iota 0) was hidden by `> 0` check; now shows "easy" for all questions/challenges

### 2026-04-03: v50 — Challenge input + chat flow
- Added `challengeInput` and `challengeChat` sub-states (mirrors learn flow)
- `i` on dashboard opens topic input instead of immediately generating
- Empty enter = random challenge (preserves old behavior)
- Topic → ollama clarifying chat → `ctrl+g` generates challenge from conversation context
- `GenerateChallengeFromChat` in ollama.go, `challengeClarifyCmd`/`generateChallengeFromChatCmd` in commands.go
- Subsequent challenges in same session reuse chat context for focused drilling
- Header shows challenge topic during input/chat phases

### 2026-04-02: v49 — Partial session saving + dashboard dedup
- `savePartialSession()` in session.go — records in-progress session; resets `sessionTotal` to prevent double-recording
- `goHome()` and `ctrl+c` now call `savePartialSession()` so esc-mid-session counts toward daily goal
- Dashboard: removed duplicate "questions today" count from top info line (already shown in "recent" section with accuracy)

### 2026-04-02: v48 — Prereq bias in review ordering
- `DependentCount()` added to DepGraph — counts direct dependents per file
- `AvgConfidence()` added to State — avg confidence across a file list
- `startReview`: when avg confidence < 3, foundational files (depended on by others, confidence ≤ 2) are promoted to front via stable sort, before prereq insertion and interleaving

### 2026-04-02: v47 — Chat UX fixes
- System prompt adapts to user feedback (removed forced-examples rule, added "follow the user's lead")
- `ctrl+l` clears chat history in overlay, quiz chat tab, challenge chat tab
- Response timing shown after each AI message `(Xms)`
- Spinner animates correctly in quiz/challenge chat tabs

### 2026-04-02: v46 — Serialize ollama requests
- Channel semaphore (capacity 1) on `chatMulti` — all requests queue instead of running in parallel. Prevents bricking session on rapid tab switches, ctrl+r spam, double-explain.

### 2026-04-02: v45 — Fix j/k eaten in quiz chat tab
- Removed j/k vim scroll bindings from quiz chat tab — they intercepted before textarea. Arrow keys scroll chat history instead.

### 2026-04-02: v44 — Challenge split view, audit reset, result UX
- Code tab splits: problem viewport (pgup/pgdown) above, textarea below
- Audit state clears on every question/challenge transition
- "You said" text on result immediately colors green/red based on grade

### 2026-04-02: v43 — Answer leak fix, syntax highlighting, bank notes, knowledge viewer
- GradeAnswer anti-leak, syntax highlighting expanded (SQL/shell/Docker/JS DOM), bank notes preview flow, knowledge viewer mode (`v`), audit→auto-fix flow

### 2026-04-01: v40-42 — Typed answer grading, codebase cleanup, keybind audit
- Ollama-graded typed answers with retry flow, GradeAnswer endpoint, auto-hint on retry
- File split: extracted commands.go + session.go (~760 lines), dedup pass
- Configurable brain path in settings, bug fixes (combo reset, export errors, default path)

### 2026-03-31: v34-39 — Challenge mode, achievements, answer retry, prereq graph
- Challenge mode (`i`): standalone coding exercises, adaptive difficulty, full XP
- 31 achievements, tiered casino bonuses, syntax highlighting
- Answer retry: MC eliminates wrong options (2 tries), typed gets educational feedback + retry
- Copy chat (ctrl+y), streak multiplier in header, challenge tabs, challenge explain (`e`)
- Session timer, time tracking in stats, prereq graph + domain interleaving

### 2026-03-30: v26-33 — Gamification overhaul, 10 question types, UI overhaul
- XP & leveling, level-up animation, casino bonus XP, favorites/focused review
- 10 question types (was 4), domain overlay fix, deterministic topic matching
- Full-width header/footer bars, terminal size guard
- Learn flow: multi-turn context, deterministic file matching, update mode

### 2026-03-27: v23-25 — boot.dev-inspired overhaul
- XP & leveling, achievements, open-notes testing, conversational learn, keybind cleanup
- Learn flow overlap detection, file index in chat, update mode for existing files

### 2026-03-23–26: v1-22 — MVP through confidence-based UX
- Initial scaffolding through domain picker, notes, smart learn, daily goal, UI cleanup
