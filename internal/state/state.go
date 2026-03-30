package state

import (
	"encoding/json"
	"math/rand"
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
	Confidence  int       `json:"confidence"` // 0=new/unrated, 1-5 user-set
	Streak      int       `json:"streak"`
}

// SessionRecord captures one completed quiz session.
type SessionRecord struct {
	Date        string   `json:"date"` // "2006-01-02"
	Correct     int      `json:"correct"`
	Wrong       int      `json:"wrong"`
	Total       int      `json:"total"`
	Domains     []string `json:"domains"`
	DurationSec int      `json:"duration_sec"`
}

// DomainStat holds aggregate stats for a knowledge domain.
type DomainStat struct {
	Domain        string
	Total         int
	AvgConfidence float64
	Weakest       string
}

// Achievement IDs
const (
	AchFirstBlood     = "first_blood"      // Answer first question
	AchScholar        = "scholar"           // Create first knowledge file
	AchOnFire3        = "on_fire_3"         // 3 day streak
	AchOnFire7        = "on_fire_7"         // 7 day streak
	AchOnFire14       = "on_fire_14"        // 14 day streak
	AchOnFire30       = "on_fire_30"        // 30 day streak
	AchCentury        = "century"           // 100 total questions
	AchThousand       = "thousand"          // 1000 total questions
	AchPerfectSession = "perfect_session"   // All confidence 4-5 in a session
	AchDeepDive       = "deep_dive"         // 5+ questions in a single lesson chat
	AchSpeedDemon     = "speed_demon"       // 10 questions in under 5 minutes
)

// AchievementInfo maps IDs to display names and descriptions.
var AchievementInfo = map[string]struct{ Name, Desc string }{
	AchFirstBlood:     {"First Blood", "answered your first question"},
	AchScholar:        {"Scholar", "created your first knowledge file"},
	AchOnFire3:        {"On Fire", "3 day streak"},
	AchOnFire7:        {"Blazing", "7 day streak"},
	AchOnFire14:       {"Unstoppable", "14 day streak"},
	AchOnFire30:       {"Legend", "30 day streak"},
	AchCentury:        {"Century", "100 questions answered"},
	AchThousand:       {"Thousand", "1000 questions answered"},
	AchPerfectSession: {"Perfect", "all confidence 4-5 in a session"},
	AchDeepDive:       {"Deep Dive", "5+ questions in a lesson chat"},
	AchSpeedDemon:     {"Speed Demon", "10 questions in under 5 minutes"},
}

// stateJSON is the on-disk format.
type stateJSON struct {
	Files           map[string]*FileState `json:"files"`
	Sessions        []SessionRecord       `json:"sessions"`
	DayStreak       int                   `json:"day_streak"`
	LastSessionDate string                `json:"last_session_date"`
	TotalXP         int                   `json:"total_xp"`
	TotalQuestions  int                   `json:"total_questions"`
	Achievements    []string              `json:"achievements,omitempty"`
}

type State struct {
	Files           map[string]*FileState
	Sessions        []SessionRecord
	DayStreak       int
	LastSessionDate string // "2006-01-02"
	TotalXP         int
	TotalQuestions  int
	Achievements    []string
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
	s.TotalXP = v2.TotalXP
	s.TotalQuestions = v2.TotalQuestions
	s.Achievements = v2.Achievements

	// Migrate: compute confidence from old correct/wrong/streak if not set
	for _, fs := range s.Files {
		if fs.Confidence == 0 && (fs.Correct > 0 || fs.Wrong > 0) {
			total := fs.Correct + fs.Wrong
			ratio := float64(fs.Correct) / float64(total)
			streakBonus := float64(fs.Streak) * 0.05
			if streakBonus > 0.3 {
				streakBonus = 0.3
			}
			strength := ratio*0.7 + streakBonus
			if strength > 1.0 {
				strength = 1.0
			}
			switch {
			case strength >= 0.8:
				fs.Confidence = 5
			case strength >= 0.6:
				fs.Confidence = 4
			case strength >= 0.4:
				fs.Confidence = 3
			case strength >= 0.2:
				fs.Confidence = 2
			default:
				fs.Confidence = 1
			}
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
		TotalXP:         s.TotalXP,
		TotalQuestions:  s.TotalQuestions,
		Achievements:    s.Achievements,
	}
	data, err := json.MarshalIndent(v2, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Record tracks a correct/wrong answer for session stats.
func (s *State) Record(path string, correct bool) {
	fs := s.ensureFile(path)
	fs.LastQuizzed = time.Now()
	if correct {
		fs.Correct++
		fs.Streak++
	} else {
		fs.Wrong++
		fs.Streak = 0
	}
}

// SetConfidence sets the user's confidence rating for a file.
func (s *State) SetConfidence(path string, level int) {
	fs := s.ensureFile(path)
	if level < 0 {
		level = 0
	}
	if level > 5 {
		level = 5
	}
	fs.Confidence = level
}

// GetConfidence returns the confidence level for a file (0=new/unrated).
func (s *State) GetConfidence(path string) int {
	fs := s.Files[path]
	if fs == nil {
		return 0
	}
	return fs.Confidence
}

// FilesByConfidence returns paths sorted by confidence ascending (0 first, then 1, 2...)
// with randomized order within each confidence tier.
func (s *State) FilesByConfidence(paths []string) []string {
	sorted := make([]string, len(paths))
	copy(sorted, paths)

	// Shuffle first so ties are random, then stable-sort by confidence.
	rand.Shuffle(len(sorted), func(i, j int) {
		sorted[i], sorted[j] = sorted[j], sorted[i]
	})
	sort.SliceStable(sorted, func(i, j int) bool {
		return s.GetConfidence(sorted[i]) < s.GetConfidence(sorted[j])
	})
	return sorted
}

func (s *State) ensureFile(path string) *FileState {
	fs, ok := s.Files[path]
	if !ok {
		fs = &FileState{Path: path}
		s.Files[path] = fs
	}
	return fs
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
		day := now.AddDate(0, 0, -(6-i)).Format("2006-01-02")
		for _, sr := range s.Sessions {
			if sr.Date == day {
				counts[i] += sr.Total
			}
		}
	}
	return counts
}

// DomainStats returns aggregate stats grouped by domain.
func (s *State) DomainStats(paths []string, domainFn func(string) string) []DomainStat {
	domains := make(map[string]*DomainStat)

	for _, p := range paths {
		d := domainFn(p)
		ds, ok := domains[d]
		if !ok {
			ds = &DomainStat{Domain: d}
			domains[d] = ds
		}
		ds.Total++

		conf := s.GetConfidence(p)
		ds.AvgConfidence += float64(conf)

		if ds.Weakest == "" || conf < s.GetConfidence(ds.Weakest) {
			ds.Weakest = p
		}
	}

	var result []DomainStat
	for _, ds := range domains {
		if ds.Total > 0 {
			ds.AvgConfidence /= float64(ds.Total)
		}
		result = append(result, *ds)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Domain < result[j].Domain
	})

	return result
}

// ResetFile clears all state for a file.
func (s *State) ResetFile(path string) {
	delete(s.Files, path)
}

// AwardXP adds XP and increments total questions count.
func (s *State) AwardXP(amount int) {
	s.TotalXP += amount
	s.TotalQuestions++
}

// AwardBonusXP adds XP without incrementing question count (for learn, etc).
func (s *State) AwardBonusXP(amount int) {
	s.TotalXP += amount
}

// Level returns the current level (0-based, level up every 100 XP).
func (s *State) Level() int {
	return s.TotalXP / 100
}

// LevelProgress returns (xp within current level, xp needed for next level).
func (s *State) LevelProgress() (int, int) {
	return s.TotalXP % 100, 100
}

// HasAchievement checks if an achievement has been unlocked.
func (s *State) HasAchievement(id string) bool {
	for _, a := range s.Achievements {
		if a == id {
			return true
		}
	}
	return false
}

// UnlockAchievement adds an achievement if not already earned. Returns true if newly unlocked.
func (s *State) UnlockAchievement(id string) bool {
	if s.HasAchievement(id) {
		return false
	}
	s.Achievements = append(s.Achievements, id)
	return true
}

// CheckAchievements checks all achievement conditions and returns newly unlocked IDs.
func (s *State) CheckAchievements(sessionTotal, sessionMinConf int, sessionDuration time.Duration, chatQuestions int) []string {
	var unlocked []string

	if s.TotalQuestions >= 1 && s.UnlockAchievement(AchFirstBlood) {
		unlocked = append(unlocked, AchFirstBlood)
	}
	if s.TotalQuestions >= 100 && s.UnlockAchievement(AchCentury) {
		unlocked = append(unlocked, AchCentury)
	}
	if s.TotalQuestions >= 1000 && s.UnlockAchievement(AchThousand) {
		unlocked = append(unlocked, AchThousand)
	}
	if s.DayStreak >= 3 && s.UnlockAchievement(AchOnFire3) {
		unlocked = append(unlocked, AchOnFire3)
	}
	if s.DayStreak >= 7 && s.UnlockAchievement(AchOnFire7) {
		unlocked = append(unlocked, AchOnFire7)
	}
	if s.DayStreak >= 14 && s.UnlockAchievement(AchOnFire14) {
		unlocked = append(unlocked, AchOnFire14)
	}
	if s.DayStreak >= 30 && s.UnlockAchievement(AchOnFire30) {
		unlocked = append(unlocked, AchOnFire30)
	}
	if sessionTotal >= 3 && sessionMinConf >= 4 && s.UnlockAchievement(AchPerfectSession) {
		unlocked = append(unlocked, AchPerfectSession)
	}
	if sessionTotal >= 10 && sessionDuration < 5*time.Minute && s.UnlockAchievement(AchSpeedDemon) {
		unlocked = append(unlocked, AchSpeedDemon)
	}
	if chatQuestions >= 5 && s.UnlockAchievement(AchDeepDive) {
		unlocked = append(unlocked, AchDeepDive)
	}

	return unlocked
}

// CalcXP computes XP earned for a question given confidence and difficulty.
func CalcXP(confidence int, diffLevel int, streakDays int) int {
	base := 10
	confBonus := confidence * 5
	diffBonus := diffLevel * 10 // 0=basic, 1=intermediate, 2=advanced
	streakBonus := streakDays * 2
	if streakBonus > 14 {
		streakBonus = 14
	}
	return base + confBonus + diffBonus + streakBonus
}
