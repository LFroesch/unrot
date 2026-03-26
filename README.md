# unrot

Quiz TUI that fights knowledge decay. Type answers, get Ollama-powered feedback, track mastery via SM-2 spaced repetition. Learn new topics, bank them to your Second Brain, quiz immediately. Daily streak tracking keeps you coming back.

## Run

```
make install       # builds + copies to ~/.local/bin/
unrot              # dashboard (default 10 questions/session)
unrot docker       # drill a specific domain
unrot -n 5         # quick 5-question session
unrot -n 0 docker  # unlimited, docker only
```

## Screens

- **Dashboard** — home screen with streak, due count, inline domain filter (tab to cycle), quick actions, recent session summary. `enter` starts review immediately.
- **Topic List** — browse/search all knowledge files with domain tabs, mastery bars. `/` to search, `+` to add, `x` to reset, `tab` to filter by domain.
- **Quiz** — single consolidated screen: lesson → question → answer → grade → explanation → next. Knowledge and chat available as overlays.
- **Learn** — type a topic (e.g. `docker/volumes`), Ollama generates a structured knowledge doc, review/save it, quiz immediately.
- **Stats** — daily streak, today's accuracy, 7-day activity chart, domains ranked by mastery %.

## Controls

| Key | Context | Action |
|-----|---------|--------|
| `enter` | dashboard | start review |
| `tab` | dashboard/topics | cycle domain filter |
| `b` | dashboard | browse topics |
| `l` | dashboard | learn something new |
| `s` | dashboard | view stats |
| `/` | topic list | search/filter |
| `+` | topic list | add new concept |
| `x` | topic list | reset SM-2 for selected |
| `j`/`k` | topic list | navigate |
| `enter` | topic list | drill selected topic |
| `enter` | question | submit answer → reveal |
| `tab` | question | reveal answer (skip typing) |
| `ctrl+e` | question | get a hint (repeatable) |
| `ctrl+r` | question | regenerate question |
| `ctrl+o` | question/result | toggle knowledge overlay |
| `ctrl+y` | question | open chat overlay |
| `a-d` | multiple choice | pick answer |
| `y`/`s`/`n` | revealed | knew it / sort of / didn't know |
| `r` | result | re-quiz same topic |
| `e` | result | explain more |
| `c` | result/lesson | open chat overlay |
| `enter` | result | next question |
| `esc` | overlay | close overlay |
| `esc` | everywhere else | back one level |
| `q` | dashboard | quit |
| `ctrl+c` | anywhere | force quit |

## Config

| Env var | Default | Purpose |
|---------|---------|---------|
| `SECOND_BRAIN` | `~/projects/active/daily_use/SECOND_BRAIN` | Path to Second Brain |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama endpoint |
| `UNROT_MODEL` | `qwen2.5:3b` | Model for question generation |

## State

Quiz history, SM-2 data, session history, and streak stored at `~/.local/share/unrot/state.json`. Auto-migrates from older format.
