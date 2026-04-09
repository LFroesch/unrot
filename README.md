# unrot

Quiz TUI that fights knowledge decay. Confidence-based review with Ollama-generated questions from Second Brain knowledge files. XP/leveling, achievements, open-notes testing, conversational learn flow, standalone coding challenges. Daily streaks keep you coming back.

## Quick Install

Supported platforms: Linux and macOS. On Windows, use WSL.

Recommended (installs to `~/.local/bin`):

```bash
curl -fsSL https://raw.githubusercontent.com/LFroesch/unrot/main/install.sh | bash
```

Or download a binary from [GitHub Releases](https://github.com/LFroesch/unrot/releases).

Or install with Go:

```bash
go install github.com/LFroesch/unrot@latest
```

Or build from source:

```bash
make install
```

Command:

```bash
unrot
```
## Run

```
make install       # builds + copies to ~/.local/bin/
unrot              # dashboard (default 10 questions/session)
unrot docker       # drill a specific domain
unrot -n 5         # quick 5-question session
unrot -n 0 docker  # unlimited, docker only
```

## Screens

- **Dashboard** — streak, confidence distribution, domain filter (tab), quick actions, recent sessions, daily goal progress
- **Topic List** — browse/search knowledge files with domain tabs, confidence dots, staleness labels, favorites (f to toggle)
- **Quiz** — teach-first flow: lesson → question → grade → result. 10 question types. Rate confidence 1-5 after each.
- **Learn** — conversational: type a topic, chat with Ollama to clarify, generate structured knowledge doc, review/save/quiz
- **Challenge** — standalone coding exercises (i from dashboard). Adaptive difficulty, Ollama-graded, full XP integration
- **Stats** — domain confidence, streaks, 7-day activity, achievements (31 total)
- **Settings** — toggle question types on/off

## Controls

| Key | Context | Action |
|-----|---------|--------|
| `enter` | dashboard | start review |
| `r` | dashboard | smart review (priority-ordered) |
| `F` | dashboard | focused review (favorites only) |
| `b` | dashboard | browse topics |
| `l` | dashboard | learn something new |
| `i` | dashboard | challenge mode |
| `s` | dashboard | settings (quiz types) |
| `a` | dashboard | stats / achievements |
| `tab` | dashboard/topics | cycle domain filter |
| `/` | topic list | search/filter |
| `f` | topic list | toggle favorite |
| `x` | topic list | reset confidence for selected |
| `j`/`k` | topic list | navigate |
| `enter` | question | submit answer |
| `tab`/`shift+tab` | question | cycle tabs (chat / quiz / knowledge) |
| `h` | MC question | hint |
| `ctrl+e` | typed question | hint (repeatable) |
| `ctrl+r` | question | regenerate question |
| `a-d` | MC/ordering | pick answer |
| `1-5` | result | rate confidence |
| `k` | result | knowledge overlay |
| `c` | result/lesson | chat overlay |
| `n` | result/lesson | notes overlay |
| `e` | result | explain more |
| `ctrl+b` | chat overlay | bank insights to notes |
| `a` | knowledge overlay | audit file accuracy |
| `ctrl+s` | challenge/notes | submit / save |
| `w` | done screen | export session report |
| `esc` | overlay | close overlay |
| `esc` | everywhere | back one level |
| `q` | dashboard | quit |

## Question Types (10)

flashcard, explain, fill-blank, finish-code, multiple-choice, compare, scenario, ordering, code-output, debug — all toggleable in settings.

## Knowledge Files

Markdown files in `SECOND_BRAIN/knowledge/<domain>/<slug>.md`. Standard sections:

- Content sections (headers, code blocks, gotchas)
- `## Connections` — related concepts + prerequisite declarations
- `## Notes` — personal notes (editable via `n` overlay)

### Prerequisites

Declare dependencies between knowledge files using `- requires: domain/slug` in the `## Connections` section:

```markdown
## Connections
- requires: go/goroutines
- requires: go/interfaces
- Channels are the primary way to communicate between goroutines
```

When starting a review, unrot checks prerequisites: if `goroutines` has low confidence (0-2), it gets inserted before `channels` in the session queue. Dependencies are resolved transitively (deepest-first) with cycle detection.

Domain interleaving ensures no more than 2 consecutive same-domain files per session (prereq pairs stay adjacent).

## Setup

Set `SECOND_BRAIN` to the root of your knowledge base (parent of `knowledge/`):

```bash
# in ~/.zshrc or ~/.bashrc
export SECOND_BRAIN="$HOME/path/to/your/second-brain"
```

Or skip the env var — on first launch unrot opens settings where you can set the path. It's saved to `state.json` so you only do it once.

## Config

| Env var | Default | Purpose |
|---------|---------|---------|
| `SECOND_BRAIN` | (none — set via env or settings) | Path to Second Brain root |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama endpoint |
| `UNROT_MODEL` | `qwen2.5:3b` | Model for question generation |
| `UNROT_DAILY_GOAL` | (unset) | Daily question goal (shows progress bar) |

## State

Confidence data, session history, streaks, achievements, and favorites stored at `~/.local/share/unrot/state.json`. Session reports exportable to `~/.local/share/unrot/reports/`.
