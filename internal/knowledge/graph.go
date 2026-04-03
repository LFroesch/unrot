package knowledge

import (
	"log"
)

// DepGraph is a directed dependency graph of knowledge file prerequisites.
type DepGraph struct {
	prereqs    map[string][]string // file → its prerequisites
	dependents map[string][]string // file → files that depend on it
	fileSet    map[string]bool     // all known files for validation
}

// BuildGraph reads all knowledge files and builds a prerequisite graph.
// Skips edges where the target file doesn't exist. Detects and removes cycles.
func BuildGraph(brainPath string, files []string) (*DepGraph, error) {
	g := &DepGraph{
		prereqs:    make(map[string][]string),
		dependents: make(map[string][]string),
		fileSet:    make(map[string]bool),
	}
	for _, f := range files {
		g.fileSet[f] = true
	}

	for _, f := range files {
		content, err := ReadFile(brainPath, f)
		if err != nil {
			continue
		}
		reqs := ParsePrereqs(content)
		for _, req := range reqs {
			if !g.fileSet[req] {
				continue // target doesn't exist, skip
			}
			if req == f {
				continue // self-reference
			}
			g.prereqs[f] = append(g.prereqs[f], req)
			g.dependents[req] = append(g.dependents[req], f)
		}
	}

	g.removeCycles()
	return g, nil
}

// PrereqsOf returns the direct prerequisites of a file.
func (g *DepGraph) PrereqsOf(path string) []string {
	if g == nil {
		return nil
	}
	return g.prereqs[path]
}

// IsPrereqOf returns true if prereq is a direct prerequisite of path.
func (g *DepGraph) IsPrereqOf(prereq, path string) bool {
	if g == nil {
		return false
	}
	for _, p := range g.prereqs[path] {
		if p == prereq {
			return true
		}
	}
	return false
}

// DependentCount returns the number of files that directly depend on path.
// A higher count means this file is more foundational. Nil-safe.
func (g *DepGraph) DependentCount(path string) int {
	if g == nil {
		return 0
	}
	return len(g.dependents[path])
}

// StalePrereqs returns transitive prerequisites with confidence at or below threshold,
// ordered deepest-first (so the most fundamental dependency comes first).
// confFn should return the confidence level (0-5) for a given file path.
func (g *DepGraph) StalePrereqs(path string, threshold int, confFn func(string) int) []string {
	if g == nil {
		return nil
	}
	var result []string
	visited := make(map[string]bool)
	g.collectStalePrereqs(path, threshold, confFn, visited, &result)
	return result
}

func (g *DepGraph) collectStalePrereqs(path string, threshold int, confFn func(string) int, visited map[string]bool, result *[]string) {
	for _, prereq := range g.prereqs[path] {
		if visited[prereq] {
			continue
		}
		visited[prereq] = true
		// Recurse first (deepest-first ordering)
		g.collectStalePrereqs(prereq, threshold, confFn, visited, result)
		// Only include if stale
		if confFn(prereq) <= threshold {
			*result = append(*result, prereq)
		}
	}
}

// removeCycles uses DFS 3-color marking to detect and remove back-edges.
func (g *DepGraph) removeCycles() {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int)

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = grey
		filtered := g.prereqs[node][:0]
		for _, prereq := range g.prereqs[node] {
			if color[prereq] == grey {
				// Back-edge = cycle, remove it
				log.Printf("unrot: removing cycle edge %s → %s", node, prereq)
				continue
			}
			filtered = append(filtered, prereq)
			if color[prereq] == white {
				dfs(prereq)
			}
		}
		g.prereqs[node] = filtered
		color[node] = black
	}

	for f := range g.fileSet {
		if color[f] == white {
			dfs(f)
		}
	}
}
