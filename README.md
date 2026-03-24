# unrot

BubbleTea quiz app that fights knowledge decay. Reads your Second Brain `knowledge/` files, uses Ollama to generate flashcard questions, tracks what's stale via spaced repetition.

## Run

```
make install  # builds + copies to ~/.local/bin/
unrot
```

## Config

| Env var | Default | Purpose |
|---------|---------|---------|
| `SECOND_BRAIN` | `~/projects/active/daily_use/SECOND_BRAIN` | Path to Second Brain |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama endpoint |
| `UNROT_MODEL` | `llama3` | Model for question generation |

## Controls

| Key | Action |
|-----|--------|
| `enter`/`space` | Reveal answer |
| `y` | Mark correct |
| `n` | Mark wrong |
| `s` | Skip question |
| `q`/`ctrl+c` | Quit |

## State

Quiz history stored at `~/.local/share/unrot/state.json`
