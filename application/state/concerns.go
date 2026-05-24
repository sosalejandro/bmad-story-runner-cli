package state

import (
	"context"
	"encoding/json"
	"fmt"
)

// AddConcernsInput is one element of the input JSON array. The schema is
// intentionally open — bmad add-concerns historically accepted the v4
// QAConcern {severity, note} shape, but the v6 store records arbitrary
// JSON-shaped payloads per row (see domain/state.Concern.BodyJSON).
//
// Callers pass either:
//
//	[{"severity":"high","note":"missing test"}]
//	[{"stage":"code-review","finding":"flaky table-driven test"}]
//
// or any other JSON-shaped object — each element is round-tripped through
// json.Marshal and stored verbatim. The CLI's Long help surfaces the
// full schema so operators don't have to grep the source.
type AddConcernsInput = map[string]any

// AddConcernsResult summarizes a successful add for the JSON envelope.
type AddConcernsResult struct {
	StoryID string `json:"story_id"`
	Added   int    `json:"added"`
	Source  string `json:"source"`
}

// AddConcerns appends one row per element of `entries` to the v6
// story_concerns table. `source` is the provenance tag (e.g. "cli",
// "code-review", "qa-gate") so downstream readers can group concerns
// by where they came from.
//
// Returns an error if the story does not exist or if any element fails
// to marshal back to JSON (which only happens for non-encodable types
// like channels or func — practically impossible from a CLI string arg).
func (s *StoryService) AddConcerns(
	ctx context.Context,
	storyID, source string,
	entries []AddConcernsInput,
) (AddConcernsResult, error) {
	if _, err := s.Stories.Get(ctx, storyID); err != nil {
		return AddConcernsResult{}, fmt.Errorf("add-concerns %q: %w", storyID, err)
	}
	for i, entry := range entries {
		body, err := json.Marshal(entry)
		if err != nil {
			return AddConcernsResult{}, fmt.Errorf("add-concerns %q: marshal entry %d: %w", storyID, i, err)
		}
		if err := s.Concerns.Add(ctx, storyID, source, string(body)); err != nil {
			return AddConcernsResult{}, fmt.Errorf("add-concerns %q: store entry %d: %w", storyID, i, err)
		}
	}
	return AddConcernsResult{StoryID: storyID, Added: len(entries), Source: source}, nil
}
