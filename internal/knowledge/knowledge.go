package knowledge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Discover finds all .md files under the knowledge/ directory of the notes root.
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

// Domain extracts the folder path under knowledge/ as the domain.
// Examples:
// - "knowledge/linux-cli/foo.md" -> "linux-cli"
// - "knowledge/projects/unrot/foo.md" -> "projects/unrot"
// - "knowledge/courses/go/basics.md" -> "courses/go"
func Domain(relPath string) string {
	parts := strings.Split(relPath, string(filepath.Separator))
	if len(parts) >= 3 && parts[0] == "knowledge" {
		return filepath.ToSlash(filepath.Join(parts[1 : len(parts)-1]...))
	}
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

// sectionContent extracts the body of a ## Heading section, stopping at the next ## heading.
func sectionContent(content, marker string) (string, bool) {
	idx := strings.Index(content, marker)
	if idx < 0 {
		return "", false
	}
	rest := content[idx+len(marker):]
	if nextH2 := strings.Index(rest, "\n## "); nextH2 >= 0 {
		rest = rest[:nextH2]
	}
	return rest, true
}

// ExtractNotes pulls the ## Notes section content from file content.
func ExtractNotes(content string) string {
	body, ok := sectionContent(content, "## Notes")
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(body, "\n"))
}

// UpdateNotes replaces or appends the ## Notes section in a knowledge file.
func UpdateNotes(brainPath, relPath, notes string) error {
	absPath := filepath.Join(brainPath, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	content := string(data)
	notes = strings.TrimSpace(notes)

	const marker = "\n## Notes\n"
	idx := strings.Index(content, marker)
	if idx >= 0 {
		before := content[:idx]
		after := content[idx+len(marker):]
		nextH2 := strings.Index(after, "\n## ")
		if nextH2 >= 0 {
			if notes == "" {
				content = before + after[nextH2:]
			} else {
				content = before + marker + notes + "\n" + after[nextH2:]
			}
		} else {
			if notes == "" {
				content = strings.TrimRight(before, "\n") + "\n"
			} else {
				content = before + marker + notes + "\n"
			}
		}
	} else if notes != "" {
		content = strings.TrimRight(content, "\n") + "\n\n## Notes\n\n" + notes + "\n"
	}

	return os.WriteFile(absPath, []byte(content), 0644)
}

// ParsePrereqs extracts prerequisite references from the ## Connections section.
// Looks for lines matching "- requires: domain/slug" and returns normalized paths.
func ParsePrereqs(content string) []string {
	rest, ok := sectionContent(content, "## Connections")
	if !ok {
		return nil
	}
	var prereqs []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- requires:") && !strings.HasPrefix(line, "* requires:") {
			continue
		}
		// Extract the value after "requires:"
		parts := strings.SplitN(line, "requires:", 2)
		if len(parts) < 2 {
			continue
		}
		ref := strings.TrimSpace(parts[1])
		if ref == "" {
			continue
		}
		prereqs = append(prereqs, ResolvePrereqPath(ref))
	}
	return prereqs
}

// ResolvePrereqPath normalizes a prerequisite reference to a relative path.
// "go/goroutines" → "knowledge/go/goroutines.md"
// "knowledge/go/goroutines.md" → "knowledge/go/goroutines.md" (pass-through)
func ResolvePrereqPath(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "knowledge/")
	ref = strings.TrimSuffix(ref, ".md")
	return "knowledge/" + ref + ".md"
}

// ReadDifficulty extracts the difficulty tag from the ## Connections section.
// Returns "easy", "medium", or "hard". Defaults to "medium" if not tagged.
func ReadDifficulty(content string) string {
	rest, ok := sectionContent(content, "## Connections")
	if !ok {
		return "medium"
	}
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- difficulty:") || strings.HasPrefix(line, "* difficulty:") {
			parts := strings.SplitN(line, "difficulty:", 2)
			if len(parts) >= 2 {
				d := strings.TrimSpace(parts[1])
				if d == "easy" || d == "medium" || d == "hard" {
					return d
				}
			}
		}
	}
	return "medium"
}

// ReadDifficultyFromFile reads difficulty from a knowledge file on disk.
// Returns "medium" on any error.
func ReadDifficultyFromFile(brainPath, relPath string) string {
	absPath := filepath.Join(brainPath, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "medium"
	}
	return ReadDifficulty(string(data))
}

// UpdateConnections writes difficulty and merged requires: lines to ## Connections.
// Existing requires: lines are preserved and merged with newConnections (no duplicates).
// difficulty replaces any existing difficulty line.
func UpdateConnections(brainPath, relPath, difficulty string, newConnections []string) error {
	absPath := filepath.Join(brainPath, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	content := string(data)

	// Merge existing + new connections (dedup)
	existing := ParsePrereqs(content)
	prereqSet := make(map[string]bool)
	for _, p := range existing {
		prereqSet[p] = true
	}
	for _, c := range newConnections {
		resolved := ResolvePrereqPath(c)
		// Skip self-references
		if resolved == relPath {
			continue
		}
		prereqSet[resolved] = true
	}

	// Build sorted list of requires lines
	var prereqSlice []string
	for p := range prereqSet {
		ref := strings.TrimPrefix(p, "knowledge/")
		ref = strings.TrimSuffix(ref, ".md")
		prereqSlice = append(prereqSlice, ref)
	}
	sort.Strings(prereqSlice)

	var lines []string
	lines = append(lines, "- difficulty: "+difficulty)
	for _, ref := range prereqSlice {
		lines = append(lines, "- requires: "+ref)
	}
	newSection := "\n\n## Connections\n" + strings.Join(lines, "\n") + "\n"

	const marker = "## Connections"
	connIdx := strings.Index(content, marker)
	if connIdx >= 0 {
		before := content[:connIdx]
		after := content[connIdx+len(marker):]
		if nextH2 := strings.Index(after, "\n## "); nextH2 >= 0 {
			content = strings.TrimRight(before, "\n") + newSection + after[nextH2:]
		} else {
			content = strings.TrimRight(before, "\n") + newSection
		}
	} else {
		content = strings.TrimRight(content, "\n") + newSection
	}

	return os.WriteFile(absPath, []byte(content), 0644)
}

// SourceMeta holds metadata from the ## Source section of project knowledge files.
type SourceMeta struct {
	Repo     string // absolute path to the source repo
	Files    string // comma-separated list of analyzed files
	Analyzed string // date string (2006-01-02)
	Commit   string // short commit hash at time of analysis
}

// ParseSource extracts ## Source metadata from a knowledge file's content.
func ParseSource(content string) *SourceMeta {
	body, ok := sectionContent(content, "## Source")
	if !ok {
		return nil
	}
	meta := &SourceMeta{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		if after, ok := strings.CutPrefix(line, "repo:"); ok {
			meta.Repo = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "files:"); ok {
			meta.Files = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "analyzed:"); ok {
			meta.Analyzed = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "commit:"); ok {
			meta.Commit = strings.TrimSpace(after)
		}
	}
	return meta
}

// FormatSource generates a ## Source markdown section from metadata.
func FormatSource(meta *SourceMeta) string {
	var b strings.Builder
	b.WriteString("## Source\n")
	b.WriteString("- repo: " + meta.Repo + "\n")
	b.WriteString("- files: " + meta.Files + "\n")
	b.WriteString("- analyzed: " + meta.Analyzed + "\n")
	b.WriteString("- commit: " + meta.Commit + "\n")
	return b.String()
}

// IsProjectDomain returns true if the domain is under the projects/ namespace.
func IsProjectDomain(domain string) bool {
	return strings.HasPrefix(domain, "projects/")
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
