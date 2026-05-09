package main

import (
	"os"
	"strings"
)

func isDemoMode() bool {
	return os.Getenv("DEMO_ENV") == "1" || os.Getenv("TUI_HUB_DEMO") == "1"
}

func isDemoOllamaError(err error) bool {
	if err == nil || !isDemoMode() {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ollama") ||
		strings.Contains(msg, "connect: connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout")
}

func demoOllamaMessage() string {
	return "Public demo mode disables Ollama-backed quiz generation and grading. Run unrot locally with Ollama for full review flows."
}
