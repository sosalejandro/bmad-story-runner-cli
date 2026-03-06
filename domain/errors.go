package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors for type-checking with errors.Is.
var (
	ErrStoryNotFound    = errors.New("story not found")
	ErrInvalidStatus    = errors.New("invalid status")
	ErrInvalidGateResult = errors.New("invalid gate result")
	ErrProgressExists   = errors.New("progress file already exists")
	ErrNoEligibleStory  = errors.New("no eligible story found")
)

// StoryNotFoundError wraps ErrStoryNotFound with the story ID.
type StoryNotFoundError struct {
	ID string
}

func (e *StoryNotFoundError) Error() string {
	return fmt.Sprintf("story %q not found in progress file", e.ID)
}

func (e *StoryNotFoundError) Is(target error) bool {
	return target == ErrStoryNotFound
}

// InvalidStatusError wraps ErrInvalidStatus with the bad value.
type InvalidStatusError struct {
	Status string
}

func (e *InvalidStatusError) Error() string {
	return fmt.Sprintf(
		"invalid status %q: must be one of: pending, in-progress, qa-review, complete, blocked",
		e.Status,
	)
}

func (e *InvalidStatusError) Is(target error) bool {
	return target == ErrInvalidStatus
}

// InvalidGateResultError wraps ErrInvalidGateResult with the bad value.
type InvalidGateResultError struct {
	Value string
}

func (e *InvalidGateResultError) Error() string {
	return fmt.Sprintf("invalid gate result %q: must be PASS, FAIL, or CONCERNS", e.Value)
}

func (e *InvalidGateResultError) Is(target error) bool {
	return target == ErrInvalidGateResult
}

// Wrap provides a consistent wrapping pattern: "context: cause".
func Wrap(context string, cause error) error {
	return fmt.Errorf("%s: %w", context, cause)
}

// Join combines multiple errors into one. Returns nil if all are nil.
func Join(errs ...error) error {
	return errors.Join(errs...)
}
