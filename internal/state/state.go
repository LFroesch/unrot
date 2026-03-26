package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type FileState struct {
	Path        string    `json:"path"`
	LastQuizzed time.Time `json:"last_quizzed"`
	Correct     int       `json:"correct"`
	Wrong       int       `json:"wrong"`
	Interval    float64   `json:"interval"`    // days until next review
	EaseFactor  float64   `json:"ease_factor"` // SM-2 ease factor (>= 1.3)
	NextReview  time.Time `json:"next_review"` // when this item is due
	Streak      int       `json:"streak"`      // consecutive correct answers
}

// SessionRecord captures one completed quiz session.
type SessionRecord struct {
	Date        string   `json:"date"`         // "2006-01-02"
	Correct     int      `json:"correct"`
	Wrong       int      `json:"wrong"`
	Total       int      `json:"total"`
	Domains     []string `json:"domains"`
	DurationSec int      `json:"duration_sec"`
}

// DueFile is a knowledge file that's due for review.
type DueFile struct {
	Path     string
	Overdue  time.Duration // how far past due (negative = not yet due)
	IsNew    bool          // never been quizzed
	Strength float64       // 0.0-1.0 mastery estimate
}

// DomainStat holds aggregate stats for a knowledge domain.
type DomainStat struct {
	Domain  string
	Total   int
	Due     int
	Mastery float64 // average strength across files
	Weakest string  // worst file in domain
}

// stateJSON is the on-disk format (v2). Handles migration from v1 (bare map).
type stateJSON struct {
	Files           map[string]*FileState `json:"files"`
	Sessions        []SessionRecord       `json:"sessions"`
	DayStreak       int                   `json:"day_streak"`
	LastSessionDate string                `json:"last_session_date"`
}

type State struct {
	Files           map[string]*FileState
	Sessions        []SessionRecord
	DayStreak       int
	LastSessionDate string // "2006-01-02"
	path            string
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

	// Try v2 format first
	var v2 stateJSON
	if err := json.Unmarshal(data, &v2); err != nil {
		return nil, err
	}

	if v2.Files != nil {
		s.Files = v2.Files
	} else {
		// v1 migration: bare map at top level
		var v1 map[string]*FileState
		if err := json.Unmarshal(data, &v1); err == nil && len(v1) > 0 {
			s.Files = v1
		}
	}

	s.Sessions = v2.Sessions
	s.DayStreak = v2.DayStreak
	s.LastSessionDate = v2.LastSessionDate

	// Migrate old entries that lack SM-2 fields
	for _, fs := range s.Files {
		if fs.EaseFactor == 0 {
			fs.EaseFactor = 2.5
		}
	}

	// Recalculate streak in case days were missed since last run
	s.refreshStreak()

	return s, nil
}

func (s *State) Save() error {
	v2 := stateJSON{
		Files:           s.Files,
		Sessions:        s.Sessions,
		DayStreak:       s.DayStreak,
		LastSessionDate: s.LastSessionDate,
	}
	data, err := json.MarshalIndent(v2, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *State) Record(path string, correct bool) {
	fs, ok := s.Files[path]
	if !ok {
		fs = &FileState{Path: path, EaseFactor: 2.5}
		s.Files[path] = fs
	}
	fs.LastQuizzed = time.Now()

	if correct {
		fs.Correct++
		fs.Streak++
		// SM-2 interval progression
		switch {
		case fs.Interval == 0:
			fs.Interval = 1
		case fs.Interval == 1:
			fs.Interval = 3
		default:
			fs.Interval = fs.Interval * fs.EaseFactor
		}
		fs.EaseFactor += 0.05
		if fs.EaseFactor > 3.0 {
			fs.EaseFactor = 3.0
		}
	} else {
		fs.Wrong++
		fs.Streak = 0
		fs.Interval = 1 // reset to 1 day
		fs.EaseFactor -= 0.2
		if fs.EaseFactor < 1.3 {
			fs.EaseFactor = 1.3
		}
	}

	fs.NextReview = fs.LastQuizzed.Add(time.Duration(fs.Interval*24) * time.Hour)
}

// RecordPartial records a "sort of" answer — counts as correct but with reduced
// interval growth and no ease factor increase. Used when the user knew the gist
// but not the details.
func (s *State) RecordPartial(path string) {
	fs, ok := s.Files[path]
	if !ok {
		fs = &FileState{Path: path, EaseFactor: 2.5}
		s.Files[path] = fs
	}
	fs.LastQuizzed = time.Now()
	fs.Correct++
	fs.Streak++

	// Reduced interval growth — roughly half the normal progression
	switch {
	case fs.Interval == 0:
		fs.Interval = 1
	case fs.Interval == 1:
		fs.Interval = 2
	default:
		fs.Interval = fs.Interval * (1 + (fs.EaseFactor-1)*0.5)
	}

	// Ease factor stays flat or drifts down slightly
	fs.EaseFactor -= 0.05
	if fs.EaseFactor < 1.3 {
		fs.EaseFactor = 1.3
	}

	fs.NextReview = fs.LastQuizzed.Add(time.Duration(fs.Interval*24) * time.Hour)
}

// RecordSession appends a session record and updates the daily streak.
func (s *State) RecordSession(correct, wrong int, domains []string, duration time.Duration) {
	today := time.Now().Format("2006-01-02")
	s.Sessions = append(s.Sessions, SessionRecord{
		Date:        today,
		Correct:     correct,
		Wrong:       wrong,
		Total:       correct + wrong,
		Domains:     domains,
		DurationSec: int(duration.Seconds()),
	})

	if s.LastSessionDate == today {
		// Already tracked today, streak unchanged
		return
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if s.LastSessionDate == yesterday {
		s.DayStreak++
	} else if s.LastSessionDate == "" {
		s.DayStreak = 1
	} else {
		s.DayStreak = 1 // streak broken
	}
	s.LastSessionDate = today
}

// refreshStreak corrects the streak if days have been missed since last session.
func (s *State) refreshStreak() {
	if s.LastSessionDate == "" {
		s.DayStreak = 0
		return
	}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if s.LastSessionDate != today && s.LastSessionDate != yesterday {
		s.DayStreak = 0
	}
}

// TodaySessions returns all sessions from today.
func (s *State) TodaySessions() []SessionRecord {
	today := time.Now().Format("2006-01-02")
	var result []SessionRecord
	for _, sr := range s.Sessions {
		if sr.Date == today {
			result = append(result, sr)
		}
	}
	return result
}

// WeekActivity returns question counts for the last 7 days (index 0 = 6 days ago, 6 = today).
func (s *State) WeekActivity() [7]int {
	var counts [7]int
	now := time.Now()
	for i := 0; i < 7; i++ {
		day := now.AddDate(0, 0, -(6 - i)).Format("2006-01-02")
		for _, sr := range s.Sessions {
			if sr.Date == day {
				counts[i] += sr.Total
			}
		}
	}
	return counts
}

// Strength returns a 0.0-1.0 mastery estimate for a file.
func (s *State) Strength(path string) float64 {
	fs := s.Files[path]
	if fs == nil {
		return 0
	}
	total := fs.Correct + fs.Wrong
	if total == 0 {
		return 0
	}
	ratio := float64(fs.Correct) / float64(total)
	streakBonus := float64(fs.Streak) * 0.05
	if streakBonus > 0.3 {
		streakBonus = 0.3
	}
	strength := ratio*0.7 + streakBonus
	if strength > 1.0 {
		strength = 1.0
	}
	return strength
}

// DueItems returns files that are due for review, sorted by most overdue first.
// Files never quizzed are always due and come first.
func (s *State) DueItems(paths []string) []DueFile {
	now := time.Now()
	var due []DueFile

	for _, p := range paths {
		fs := s.Files[p]
		if fs == nil {
			due = append(due, DueFile{Path: p, IsNew: true, Strength: 0})
			continue
		}
		overdue := now.Sub(fs.NextReview)
		if overdue >= 0 {
			due = append(due, DueFile{
				Path:     p,
				Overdue:  overdue,
				Strength: s.Strength(p),
			})
		}
	}

	sort.Slice(due, func(i, j int) bool {
		if due[i].IsNew != due[j].IsNew {
			return due[i].IsNew
		}
		return due[i].Overdue > due[j].Overdue
	})

	return due
}

// DomainStats returns aggregate stats grouped by domain.
func (s *State) DomainStats(paths []string, domainFn func(string) string) []DomainStat {
	now := time.Now()
	domains := make(map[string]*DomainStat)

	for _, p := range paths {
		d := domainFn(p)
		ds, ok := domains[d]
		if !ok {
			ds = &DomainStat{Domain: d}
			domains[d] = ds
		}
		ds.Total++

		strength := s.Strength(p)
		ds.Mastery += strength

		fs := s.Files[p]
		if fs == nil || now.After(fs.NextReview) {
			ds.Due++
		}

		if ds.Weakest == "" || strength < s.Strength(ds.Weakest) {
			ds.Weakest = p
		}
	}

	var result []DomainStat
	for _, ds := range domains {
		if ds.Total > 0 {
			ds.Mastery /= float64(ds.Total)
		}
		result = append(result, *ds)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Domain < result[j].Domain
	})

	return result
}

// ResetFile clears all SM-2 state for a file, making it due for review immediately.
func (s *State) ResetFile(path string) {
	delete(s.Files, path)
}

// Stalest returns knowledge file paths sorted by staleness (least recently quizzed first).
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
		return fi.LastQuizzed.Before(fj.LastQuizzed)
	})
	return sorted
}
