package knowledge

import "testing"

func TestDomainSupportsNestedFolders(t *testing.T) {
	tests := map[string]string{
		"knowledge/go/goroutines.md":                "go",
		"knowledge/projects/unrot/state-machine.md": "projects/unrot",
		"knowledge/courses/go/interfaces/basics.md": "courses/go/interfaces",
		"knowledge/linux-cli/foo.md":                "linux-cli",
	}

	for path, want := range tests {
		if got := Domain(path); got != want {
			t.Fatalf("Domain(%q) = %q, want %q", path, got, want)
		}
	}
}
