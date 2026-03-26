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
			name := filepath.Base(path)
			if strings.ToUpper(name) == "INDEX.MD" || strings.HasPrefix(name, ".") {
				return nil
			}
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

// WriteFile writes content to a knowledge file, creating directories as needed.
func WriteFile(brainPath, domain, slug, content string) (string, error) {
	dir := filepath.Join(brainPath, "knowledge", domain)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	relPath := filepath.Join("knowledge", domain, slug+".md")
	absPath := filepath.Join(brainPath, relPath)
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return relPath, nil
}

// Domain extracts the domain from a relative path (e.g., "knowledge/linux-cli/foo.md" -> "linux-cli").
func Domain(relPath string) string {
	parts := strings.Split(relPath, string(filepath.Separator))
	if len(parts) >= 2 {
		return parts[1]
	}
	return "general"
}

// Domains returns a deduplicated list of domains found in the given paths.
func Domains(paths []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, p := range paths {
		d := Domain(p)
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	return result
}

// FilterByDomain returns only paths matching the given domain.
func FilterByDomain(paths []string, domain string) []string {
	var result []string
	for _, p := range paths {
		if Domain(p) == domain {
			result = append(result, p)
		}
	}
	return result
}
