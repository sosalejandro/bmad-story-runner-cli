// Package migrate handles V4 progress.json → V6 sqlite import (one-shot,
// idempotent). The V4 store remains read-only after migration completes.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	v4 "github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// V4ToV6 reads a v4 progress.json, projects its rows into v6 sqlite tables,
// and returns a summary of what landed.
type V4ToV6 struct {
	Stories      state.Stories
	Dependencies state.StoryDependencies
	Concerns     state.StoryConcerns
	Config       state.Config
}

// Result summarises one migrate run. Re-runs against the same progress.json
// produce the same final state but with all counters at zero (idempotent —
// existing rows preserved, missing rows added).
type Result struct {
	StoriesInserted   int      `json:"stories_inserted"`
	StoriesSkipped    int      `json:"stories_skipped"`
	DependenciesAdded int      `json:"dependencies_added"`
	ConcernsAdded     int      `json:"concerns_added"`
	DocsFolder        string   `json:"docs_folder"`
	SourceFile        string   `json:"source_file"`
	Warnings          []string `json:"warnings,omitempty"`
}

// Migrate parses progressPath and writes its rows into v6 sqlite via the
// injected ports. Idempotent: stories already present skip re-insert.
func (m *V4ToV6) Migrate(ctx context.Context, progressPath string) (*Result, error) {
	raw, err := os.ReadFile(progressPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", progressPath, err)
	}
	var pf v4.ProgressFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", progressPath, err)
	}

	res := &Result{
		DocsFolder: pf.DocsFolder,
		SourceFile: progressPath,
	}

	if pf.DocsFolder != "" {
		if err := m.Config.Set(ctx, "docs_folder", pf.DocsFolder); err != nil {
			return nil, fmt.Errorf("migrate set docs_folder: %w", err)
		}
	}

	for _, src := range pf.Stories {
		if src == nil {
			continue
		}
		if _, err := m.Stories.Get(ctx, src.ID); err == nil {
			res.StoriesSkipped++
			continue
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, fmt.Errorf("migrate probe %q: %w", src.ID, err)
		}

		st := mapV4Story(src)
		if err := m.Stories.Insert(ctx, st); err != nil {
			return nil, fmt.Errorf("migrate insert %q: %w", src.ID, err)
		}
		res.StoriesInserted++

		for _, b := range src.Blockers {
			if b == "" {
				continue
			}
			if err := m.Dependencies.Add(ctx, src.ID, b); err != nil {
				return nil, fmt.Errorf("migrate dep %q→%q: %w", src.ID, b, err)
			}
			res.DependenciesAdded++
		}

		for _, c := range src.QAConcerns {
			body, _ := json.Marshal(c)
			if err := m.Concerns.Add(ctx, src.ID, "v4-migrate", string(body)); err != nil {
				return nil, fmt.Errorf("migrate concern %q: %w", src.ID, err)
			}
			res.ConcernsAdded++
		}
	}

	return res, nil
}

func mapV4Story(s *v4.Story) state.Story {
	out := state.Story{
		ID:         s.ID,
		File:       s.File,
		Title:      s.Title,
		Status:     mapV4Status(s.Status),
		Complexity: state.ComplexityMedium,
		CIPassed:   s.CIPassed,
		ParallelGroup: s.ParallelGroup,
	}
	if s.CompletedAt != nil {
		t := *s.CompletedAt
		out.CompletedAt = &t
	}
	return out
}

// mapV4Status converts the v4 status set into v6 names. The v4 `qa-review`
// state has no exact v6 analog — closest fit is "reviewing".
func mapV4Status(v4Status v4.Status) state.Status {
	switch v4Status {
	case v4.StatusPending:
		return state.StatusPending
	case v4.StatusInProgress:
		return state.StatusInProgress
	case v4.StatusQAReview:
		return state.StatusReviewing
	case v4.StatusComplete:
		return state.StatusComplete
	case v4.StatusBlocked:
		return state.StatusBlocked
	default:
		return state.StatusPending
	}
}
