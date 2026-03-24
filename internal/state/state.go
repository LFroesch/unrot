package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type FileState struct {
	Path       string    `json:"path"`
	LastQuized time.Time `json:"last_quizzed"`
	Correct    int       `json:"correct"`
	Wrong      int       `json:"wrong"`
}

type State struct {
	Files map[string]*FileState `json:"files"`
	path  string
}

func Load() (*State, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".local", "share", "unrot")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "state.json")

	s := &State{
		Files: make(map[string]*FileState),
		path:  p,
	}

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &s.Files); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *State) Save() error {
	data, err := json.MarshalIndent(s.Files, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *State) Record(path string, correct bool) {
	fs, ok := s.Files[path]
	if !ok {
		fs = &FileState{Path: path}
		s.Files[path] = fs
	}
	fs.LastQuized = time.Now()
	if correct {
		fs.Correct++
	} else {
		fs.Wrong++
	}
}

// Stalest returns knowledge file paths sorted by staleness (least recently quizzed first).
// Files not yet quizzed come first.
func (s *State) Stalest(paths []string) []string {
	sorted := make([]string, len(paths))
	copy(sorted, paths)

	sort.Slice(sorted, func(i, j int) bool {
		fi := s.Files[sorted[i]]
		fj := s.Files[sorted[j]]
		if fi == nil {
			return true
		}
		if fj == nil {
			return false
		}
		return fi.LastQuized.Before(fj.LastQuized)
	})
	return sorted
}
