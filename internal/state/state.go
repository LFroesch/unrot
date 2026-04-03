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
	Path         string    `json:"path"`
	LastQuizzed  time.Time `json:"last_quizzed"`
	LastReviewed time.Time `json:"last_reviewed,omitempty"` // set on question generation (opened the topic)
	Correct      int       `json:"correct"`
	Wrong        int       `json:"wrong"`
	Confidence   int       `json:"confidence"` // 0=new/unrated, 1-5 user-set
	Streak       int       `json:"streak"`
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
	// Milestones
	AchFirstBlood = "first_blood" // Answer first question
	AchHalfCent   = "half_cent"   // 50 total questions
	AchCentury    = "century"     // 100 total questions
	AchFiveHundo  = "five_hundo"  // 500 total questions
	AchThousand   = "thousand"    // 1000 total questions

	// Streaks
	AchOnFire3  = "on_fire_3"  // 3 day streak
	AchOnFire7  = "on_fire_7"  // 7 day streak
	AchOnFire14 = "on_fire_14" // 14 day streak
	AchOnFire30 = "on_fire_30" // 30 day streak

	// Combos
	AchCombo3  = "combo_3"  // 3 correct in a row
	AchCombo5  = "combo_5"  // 5 correct in a row
	AchCombo10 = "combo_10" // 10 correct in a row
	AchCombo20 = "combo_20" // 20 correct in a row

	// Session
	AchPerfectSession = "perfect_session" // All confidence 4-5 in a session
	AchSpeedDemon     = "speed_demon"     // 10 questions in under 5 minutes
	AchMarathon       = "marathon"        // 20+ questions in a session
	AchDeepDive       = "deep_dive"       // 5+ questions in a single lesson chat

	// Learning
	AchScholar      = "scholar"      // Create first knowledge file
	AchLibrarian    = "librarian"    // 25+ knowledge files
	AchEncyclopedia = "encyclopedia" // 100+ knowledge files

	// Domains
	AchExplorer  = "explorer"  // 3+ domains in one session
	AchPolyglot  = "polyglot"  // 5+ domains in one session
	AchSpecialist = "specialist" // all files in a domain at confidence 4+

	// Confidence
	AchFirstLock = "first_lock" // First file at confidence 5
	AchTenLocked = "ten_locked" // 10 files at confidence 5

	// Fun / rare
	AchJackpot    = "jackpot"    // Hit a jackpot bonus
	AchNightOwl   = "night_owl"  // Session after midnight
	AchEarlyBird  = "early_bird" // Session before 7am
	AchComeback   = "comeback"   // Wrong answer → retry → correct on same file
	AchChatterbox = "chatterbox" // 10+ messages in one chat session
)

// AchievementInfo maps IDs to display names and descriptions.
var AchievementInfo = map[string]struct{ Name, Desc string }{
	// Milestones
	AchFirstBlood: {"First Blood", "answered your first question"},
	AchHalfCent:   {"Fifty", "50 questions answered"},
	AchCentury:    {"Century", "100 questions answered"},
	AchFiveHundo:  {"Five Hundred", "500 questions answered"},
	AchThousand:   {"Thousand", "1000 questions answered"},
	// Streaks
	AchOnFire3:  {"On Fire", "3 day streak"},
	AchOnFire7:  {"Blazing", "7 day streak"},
	AchOnFire14: {"Unstoppable", "14 day streak"},
	AchOnFire30: {"Legend", "30 day streak"},
	// Combos
	AchCombo3:  {"Triple", "3 correct in a row"},
	AchCombo5:  {"Hot Streak", "5 correct in a row"},
	AchCombo10: {"On a Roll", "10 correct in a row"},
	AchCombo20: {"Flawless", "20 correct in a row"},
	// Session
	AchPerfectSession: {"Perfect", "all confidence 4-5 in a session"},
	AchSpeedDemon:     {"Speed Demon", "10 questions in under 5 minutes"},
	AchMarathon:       {"Marathon", "20+ questions in a session"},
	AchDeepDive:       {"Deep Dive", "5+ questions in a lesson chat"},
	// Learning
	AchScholar:      {"Scholar", "created your first knowledge file"},
	AchLibrarian:    {"Librarian", "25+ knowledge files in your bank"},
	AchEncyclopedia: {"Encyclopedia", "100+ knowledge files"},
	// Domains
	AchExplorer:  {"Explorer", "3+ domains in one session"},
	AchPolyglot:  {"Polyglot", "5+ domains in one session"},
	AchSpecialist: {"Specialist", "mastered a domain (all files 4+)"},
	// Confidence
	AchFirstLock: {"Locksmith", "first file at confidence 5"},
	AchTenLocked: {"Vault", "10 files locked at confidence 5"},
	// Fun / rare
	AchJackpot:    {"Jackpot", "hit a rare bonus drop"},
	AchNightOwl:   {"Night Owl", "studying past midnight"},
	AchEarlyBird:  {"Early Bird", "studying before 7am"},
	AchComeback:   {"Comeback", "wrong → retry → nailed it"},
	AchChatterbox: {"Chatterbox", "10+ messages in one chat"},
}

// AllAchievements returns all achievement IDs in display order.
var AllAchievements = []string{
	// Milestones
	AchFirstBlood, AchHalfCent, AchCentury, AchFiveHundo, AchThousand,
	// Streaks
	AchOnFire3, AchOnFire7, AchOnFire14, AchOnFire30,
	// Combos
	AchCombo3, AchCombo5, AchCombo10, AchCombo20,
	// Session
	AchPerfectSession, AchSpeedDemon, AchMarathon, AchDeepDive,
	// Learning
	AchScholar, AchLibrarian, AchEncyclopedia,
	// Domains
	AchExplorer, AchPolyglot, AchSpecialist,
	// Confidence
	AchFirstLock, AchTenLocked,
	// Fun / rare
	AchJackpot, AchNightOwl, AchEarlyBird, AchComeback, AchChatterbox,
}

// AchievementContext holds everything needed to check achievement conditions.
type AchievementContext struct {
	SessionTotal    int
	SessionMinConf  int
	SessionDuration time.Duration
	ChatQuestions   int
	ComboMax        int
	DomainCount     int
	FileCount       int
	LockedCount     int  // files at confidence 5
	HitJackpot      bool // hit a jackpot bonus this question
	IsComeback      bool // wrong → retry → correct on same file
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
	Favorites       []string              `json:"favorites,omitempty"`
	MaxQuestions    int                   `json:"max_questions,omitempty"`
	ChallengeDiff   int                   `json:"challenge_diff,omitempty"` // 0=adaptive, 1=basic, 2=intermediate, 3=advanced
	BrainPath       string                `json:"brain_path,omitempty"`
	LogCalls        bool                  `json:"log_calls,omitempty"`
}

type State struct {
	Files           map[string]*FileState
	Sessions        []SessionRecord
	DayStreak       int
	LastSessionDate string // "2006-01-02"
	TotalXP         int
	TotalQuestions  int
	Achievements    []string
	Favorites       map[string]bool
	MaxQuestions    int    // 0 = use default (5)
	ChallengeDiff   int    // 0=adaptive, 1=basic, 2=intermediate, 3=advanced
	BrainPath       string // user-configured knowledge path
	LogCalls        bool   // log all ollama requests/responses to file
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
	s.Favorites = make(map[string]bool)
	for _, f := range v2.Favorites {
		s.Favorites[f] = true
	}
	s.MaxQuestions = v2.MaxQuestions
	s.ChallengeDiff = v2.ChallengeDiff
	s.BrainPath = v2.BrainPath
	s.LogCalls = v2.LogCalls

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
	var favs []string
	for f := range s.Favorites {
		favs = append(favs, f)
	}
	sort.Strings(favs)
	v2 := stateJSON{
		Files:           s.Files,
		Sessions:        s.Sessions,
		DayStreak:       s.DayStreak,
		LastSessionDate: s.LastSessionDate,
		TotalXP:         s.TotalXP,
		TotalQuestions:  s.TotalQuestions,
		Achievements:    s.Achievements,
		Favorites:       favs,
		MaxQuestions:    s.MaxQuestions,
		ChallengeDiff:   s.ChallengeDiff,
		BrainPath:       s.BrainPath,
		LogCalls:        s.LogCalls,
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

// MarkReviewed sets the LastReviewed timestamp for a file (called on question generation).
func (s *State) MarkReviewed(path string) {
	fs := s.ensureFile(path)
	fs.LastReviewed = time.Now()
}

// StaleDays returns days since a file was last reviewed (or -1 if never reviewed).
func (s *State) StaleDays(path string) int {
	fs := s.Files[path]
	if fs == nil || fs.LastReviewed.IsZero() {
		return -1
	}
	return int(time.Since(fs.LastReviewed).Hours() / 24)
}

// FilesByPriority returns paths sorted by review priority (highest priority first).
// Priority blends: never-seen > low confidence > stale high-confidence > fresh high-confidence.
// Within similar priority, order is randomized.
func (s *State) FilesByPriority(paths []string) []string {
	sorted := make([]string, len(paths))
	copy(sorted, paths)

	// Shuffle first so ties are random
	rand.Shuffle(len(sorted), func(i, j int) {
		sorted[i], sorted[j] = sorted[j], sorted[i]
	})

	sort.SliceStable(sorted, func(i, j int) bool {
		return s.priorityScore(sorted[i]) > s.priorityScore(sorted[j])
	})
	return sorted
}

// priorityScore computes a review priority (higher = more urgent).
// Never-seen files get highest priority, then low confidence, then staleness boosts.
func (s *State) priorityScore(path string) float64 {
	fs := s.Files[path]

	// Never seen at all — highest priority
	if fs == nil || (fs.Confidence == 0 && fs.LastReviewed.IsZero()) {
		return 100
	}

	// Base: inverse confidence (conf 1 = 50, conf 5 = 10)
	score := float64(6-fs.Confidence) * 10

	// Staleness boost: days since last review, scaled by confidence
	// High confidence items get bigger staleness boost (they're the ones hiding at the bottom)
	if !fs.LastReviewed.IsZero() {
		days := time.Since(fs.LastReviewed).Hours() / 24
		// Confidence 5 stale 30 days: +15 boost. Confidence 1 stale 30 days: +3 boost.
		staleBoost := days * float64(fs.Confidence) * 0.1
		if staleBoost > 30 {
			staleBoost = 30
		}
		score += staleBoost
	} else {
		// Has been quizzed (has confidence) but no LastReviewed — treat as moderately stale
		score += 10
	}

	return score
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

// AvgConfidence returns the average confidence across the given paths.
// Unrated files (confidence 0) are included as 0.
func (s *State) AvgConfidence(paths []string) float64 {
	if len(paths) == 0 {
		return 0
	}
	var total int
	for _, p := range paths {
		total += s.GetConfidence(p)
	}
	return float64(total) / float64(len(paths))
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

// ToggleFavorite toggles a file's favorite status. Returns new state.
func (s *State) ToggleFavorite(path string) bool {
	if s.Favorites == nil {
		s.Favorites = make(map[string]bool)
	}
	if s.Favorites[path] {
		delete(s.Favorites, path)
		return false
	}
	s.Favorites[path] = true
	return true
}

// IsFavorite returns whether a file is favorited.
func (s *State) IsFavorite(path string) bool {
	return s.Favorites[path]
}

// FavoritePaths returns all favorited file paths that exist in the given file list.
func (s *State) FavoritePaths(allFiles []string) []string {
	var result []string
	for _, f := range allFiles {
		if s.Favorites[f] {
			result = append(result, f)
		}
	}
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

// Level returns the current level (0-based, exponential curve).
func (s *State) Level() int {
	return LevelFromXP(s.TotalXP)
}

// LevelProgress returns (xp within current level, xp needed to reach next level from current).
func (s *State) LevelProgress() (int, int) {
	lvl := s.Level()
	currentThreshold := XPForLevel(lvl)
	nextThreshold := XPForLevel(lvl + 1)
	return s.TotalXP - currentThreshold, nextThreshold - currentThreshold
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
func (s *State) CheckAchievements(ctx AchievementContext) []string {
	var unlocked []string

	tryUnlock := func(id string) {
		if s.UnlockAchievement(id) {
			unlocked = append(unlocked, id)
		}
	}

	// Milestones
	if s.TotalQuestions >= 1 {
		tryUnlock(AchFirstBlood)
	}
	if s.TotalQuestions >= 50 {
		tryUnlock(AchHalfCent)
	}
	if s.TotalQuestions >= 100 {
		tryUnlock(AchCentury)
	}
	if s.TotalQuestions >= 500 {
		tryUnlock(AchFiveHundo)
	}
	if s.TotalQuestions >= 1000 {
		tryUnlock(AchThousand)
	}

	// Streaks
	if s.DayStreak >= 3 {
		tryUnlock(AchOnFire3)
	}
	if s.DayStreak >= 7 {
		tryUnlock(AchOnFire7)
	}
	if s.DayStreak >= 14 {
		tryUnlock(AchOnFire14)
	}
	if s.DayStreak >= 30 {
		tryUnlock(AchOnFire30)
	}

	// Combos
	if ctx.ComboMax >= 3 {
		tryUnlock(AchCombo3)
	}
	if ctx.ComboMax >= 5 {
		tryUnlock(AchCombo5)
	}
	if ctx.ComboMax >= 10 {
		tryUnlock(AchCombo10)
	}
	if ctx.ComboMax >= 20 {
		tryUnlock(AchCombo20)
	}

	// Session
	if ctx.SessionTotal >= 3 && ctx.SessionMinConf >= 4 {
		tryUnlock(AchPerfectSession)
	}
	if ctx.SessionTotal >= 10 && ctx.SessionDuration < 5*time.Minute {
		tryUnlock(AchSpeedDemon)
	}
	if ctx.SessionTotal >= 20 {
		tryUnlock(AchMarathon)
	}
	if ctx.ChatQuestions >= 5 {
		tryUnlock(AchDeepDive)
	}
	if ctx.ChatQuestions >= 10 {
		tryUnlock(AchChatterbox)
	}

	// Learning
	if ctx.FileCount >= 25 {
		tryUnlock(AchLibrarian)
	}
	if ctx.FileCount >= 100 {
		tryUnlock(AchEncyclopedia)
	}

	// Domains
	if ctx.DomainCount >= 3 {
		tryUnlock(AchExplorer)
	}
	if ctx.DomainCount >= 5 {
		tryUnlock(AchPolyglot)
	}

	// Confidence
	if ctx.LockedCount >= 1 {
		tryUnlock(AchFirstLock)
	}
	if ctx.LockedCount >= 10 {
		tryUnlock(AchTenLocked)
	}

	// Fun / rare
	if ctx.HitJackpot {
		tryUnlock(AchJackpot)
	}
	if ctx.IsComeback {
		tryUnlock(AchComeback)
	}

	// Time-based
	hour := time.Now().Hour()
	if hour >= 0 && hour < 5 {
		tryUnlock(AchNightOwl)
	}
	if hour >= 5 && hour < 7 {
		tryUnlock(AchEarlyBird)
	}

	return unlocked
}

// CountLocked returns the number of files at confidence 5.
func (s *State) CountLocked() int {
	count := 0
	for _, fs := range s.Files {
		if fs.Confidence >= 5 {
			count++
		}
	}
	return count
}

// IsDomainMastered returns true if all files in a domain are at confidence 4+.
func (s *State) IsDomainMastered(paths []string, domainFn func(string) string, domain string) bool {
	count := 0
	for _, p := range paths {
		if domainFn(p) == domain {
			count++
			if s.GetConfidence(p) < 4 {
				return false
			}
		}
	}
	return count > 0
}

// CalcXP computes XP earned for a question given confidence, difficulty, streak, and staleness.
func CalcXP(confidence int, diffLevel int, streakDays int, staleDays int) (total int, breakdown XPBreakdown) {
	breakdown.Base = 15
	breakdown.Confidence = confidence * 5
	breakdown.Difficulty = diffLevel * 10 // 0=basic, 1=intermediate, 2=advanced

	// Streak multiplier: 1.0x base, +0.1x per day, max 2.0x
	breakdown.StreakMultiplier = 1.0 + float64(streakDays)*0.1
	if breakdown.StreakMultiplier > 2.0 {
		breakdown.StreakMultiplier = 2.0
	}

	// Staleness bonus: reviewing something you haven't touched in a while
	if staleDays > 7 {
		breakdown.Staleness = (staleDays - 7) / 2
		if breakdown.Staleness > 20 {
			breakdown.Staleness = 20
		}
	}

	subtotal := float64(breakdown.Base+breakdown.Confidence+breakdown.Difficulty+breakdown.Staleness) * breakdown.StreakMultiplier
	total = int(subtotal)
	if total < 1 {
		total = 1
	}
	breakdown.Total = total
	return total, breakdown
}

// XPBreakdown shows how XP was calculated (for display on result screen).
type XPBreakdown struct {
	Base             int
	Confidence       int
	Difficulty       int
	Staleness        int
	Bonus            int     // casino bonus drop
	StreakMultiplier  float64
	Total            int
}

// XPForLevel returns total XP needed to reach a given level (exponential curve).
// Level 1 = 40, Level 2 = 120, Level 3 = 240, etc.
func XPForLevel(level int) int {
	if level <= 0 {
		return 0
	}
	return 40 * level * (level + 1) / 2
}

// LevelFromXP returns the current level for a given total XP (inverse of exponential curve).
func LevelFromXP(xp int) int {
	level := 0
	for XPForLevel(level+1) <= xp {
		level++
	}
	return level
}
