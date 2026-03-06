package domain

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in-progress"
	StatusQAReview   Status = "qa-review"
	StatusComplete   Status = "complete"
	StatusBlocked    Status = "blocked"
)

var validStatuses = map[Status]bool{
	StatusPending:    true,
	StatusInProgress: true,
	StatusQAReview:   true,
	StatusComplete:   true,
	StatusBlocked:    true,
}

func ParseStatus(s string) (Status, error) {
	st := Status(s)
	if !validStatuses[st] {
		return "", &InvalidStatusError{Status: s}
	}
	return st, nil
}

// StatusFromFileHint maps free-form story file status strings to canonical Status values.
var StatusFromFileHint = map[string]Status{
	"done":                 StatusComplete,
	"complete":             StatusComplete,
	"completed":            StatusComplete,
	"implemented":          StatusComplete,
	"pass":                 StatusComplete,
	"pass with observations": StatusComplete,
	"ready for done":       StatusQAReview,
	"ready for review":     StatusQAReview,
	"review":               StatusQAReview,
	"approved":             StatusPending,
	"ready":                StatusPending,
	"draft":                StatusPending,
	"fail":                 StatusInProgress,
	"concerns":             StatusInProgress,
}

type QAConcern struct {
	Severity string `json:"severity"`
	Note     string `json:"note"`
}

type Story struct {
	ID             string       `json:"id"`
	File           string       `json:"file"`
	Title          string       `json:"title"`
	Status         Status       `json:"status"`
	ParallelGroup  *int         `json:"parallel_group"`
	AssignedSession *string     `json:"assigned_session"`
	Blockers       []string     `json:"blockers"`
	QAConcerns     []QAConcern  `json:"qa_concerns"`
	CIPassed       bool         `json:"ci_passed"`
	CompletedAt    *time.Time   `json:"completed_at"`
}

// SortKey returns a numeric sort key from the story filename prefix (e.g. "2.8.foo.md" -> [2, 8]).
func (s *Story) SortKey() []int {
	name := filepath.Base(s.File)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	parts := strings.Split(name, ".")
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return []int{999}
	}
	return nums
}

// IsEligible returns true if the story can be picked up: status is pending and all blockers are complete.
func (s *Story) IsEligible(completeIDs map[string]bool) bool {
	if s.Status != StatusPending {
		return false
	}
	for _, b := range s.Blockers {
		if !completeIDs[b] {
			return false
		}
	}
	return true
}
