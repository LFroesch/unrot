package knowledge

import (
	"os"
	"path/filepath"
	"strings"
)

// Discover finds all .md files under the knowledge/ directory of the Second Brain.
func Discover(brainPath string) ([]string, error) {
	knowledgeDir := filepath.Join(brainPath, "knowledge")
	var files []string

	err := filepath.Walk(knowledgeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			// Store relative to brain root for display
			rel, _ := filepath.Rel(brainPath, path)
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// ReadFile reads the content of a knowledge file.
func ReadFile(brainPath, relPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(brainPath, relPath))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Domain extracts the domain from a relative path (e.g., "knowledge/linux-cli/foo.md" -> "linux-cli").
func Domain(relPath string) string {
	parts := strings.Split(relPath, string(filepath.Separator))
	if len(parts) >= 2 {
		return parts[1]
	}
	return "general"
}
