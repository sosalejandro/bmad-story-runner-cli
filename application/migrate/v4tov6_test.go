package migrate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/migrate"
	v4 "github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func writeV4(t *testing.T, pf v4.ProgressFile) string {
	t.Helper()
	raw, _ := json.Marshal(pf)
	path := filepath.Join(t.TempDir(), "bmad-progress.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write v4: %v", err)
	}
	return path
}

func newMigrator(t *testing.T) (*migrate.V4ToV6, *sqlite.DB) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &migrate.V4ToV6{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Concerns:     sqlite.NewStoryConcernsStore(db),
		Config:       sqlite.NewConfigStore(db),
	}, db
}

func intPtr(n int) *int { return &n }

func TestMigrate_ImportsStoriesAndDeps(t *testing.T) {
	t.Parallel()
	m, db := newMigrator(t)
	path := writeV4(t, v4.ProgressFile{
		Version:    1,
		DocsFolder: "/docs",
		Stories: []*v4.Story{
			{ID: "1.1", File: "1.1.md", Title: "One", Status: v4.StatusComplete, CIPassed: true, ParallelGroup: intPtr(1)},
			{ID: "1.2", File: "1.2.md", Title: "Two", Status: v4.StatusPending, Blockers: []string{"1.1"}},
		},
	})

	res, err := m.Migrate(context.Background(), path)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if res.StoriesInserted != 2 || res.DependenciesAdded != 1 {
		t.Fatalf("counters wrong: %+v", res)
	}
	stories := sqlite.NewStoriesStore(db)
	st, _ := stories.Get(context.Background(), "1.1")
	if st.Status != state.StatusComplete || !st.CIPassed {
		t.Fatalf("1.1 round-trip wrong: %+v", st)
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	t.Parallel()
	m, _ := newMigrator(t)
	path := writeV4(t, v4.ProgressFile{
		Stories: []*v4.Story{
			{ID: "a", File: "a.md", Status: v4.StatusPending},
		},
	})

	first, _ := m.Migrate(context.Background(), path)
	second, _ := m.Migrate(context.Background(), path)
	if first.StoriesInserted != 1 || second.StoriesInserted != 0 {
		t.Fatalf("idempotency broken: first %+v, second %+v", first, second)
	}
	if second.StoriesSkipped != 1 {
		t.Fatalf("expected skip on re-run, got %+v", second)
	}
}

func TestMigrate_ImportsQAConcerns(t *testing.T) {
	t.Parallel()
	m, db := newMigrator(t)
	path := writeV4(t, v4.ProgressFile{
		Stories: []*v4.Story{
			{ID: "x", File: "x.md", Status: v4.StatusBlocked,
				QAConcerns: []v4.QAConcern{
					{Severity: "high", Note: "flaky test in foo_test.go"},
				}},
		},
	})
	res, _ := m.Migrate(context.Background(), path)
	if res.ConcernsAdded != 1 {
		t.Fatalf("ConcernsAdded = %d, want 1", res.ConcernsAdded)
	}
	concerns, _ := sqlite.NewStoryConcernsStore(db).Of(context.Background(), "x")
	if len(concerns) != 1 || concerns[0].Source != "v4-migrate" {
		t.Fatalf("concern row not landed correctly: %+v", concerns)
	}
}
