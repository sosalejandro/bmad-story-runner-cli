package sprint_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// BenchmarkGraphBuild_190Stories backs acceptance criterion #1:
// `bmad sprint graph --format dot` runs in <500ms on a 190-story DB.
//
// We seed 22 epics × ~9 stories each = 198 stories with a realistic
// intra-epic chain (Y → Y-1) and an inter-epic edge per epic.
func BenchmarkGraphBuild_190Stories(b *testing.B) {
	db := openDBForBench(b)
	storiesStore := sqlite.NewStoriesStore(db)
	depsStore := sqlite.NewStoryDependenciesStore(db)
	ctx := context.Background()
	now := time.Now()
	for epic := 1; epic <= 22; epic++ {
		for y := 1; y <= 9; y++ {
			id := fmt.Sprintf("%d.%d", epic, y)
			if err := storiesStore.Insert(ctx, state.Story{
				ID: id, Title: fmt.Sprintf("epic %d story %d", epic, y),
				Status: state.StatusPending, Complexity: state.ComplexityMedium,
				StoryType: state.StoryTypeCode, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				b.Fatalf("seed %s: %v", id, err)
			}
			if y > 1 {
				if err := depsStore.Add(ctx, id, fmt.Sprintf("%d.%d", epic, y-1)); err != nil {
					b.Fatalf("dep: %v", err)
				}
			}
			if epic > 1 && y == 1 {
				if err := depsStore.Add(ctx, id, fmt.Sprintf("%d.9", epic-1)); err != nil {
					b.Fatalf("inter-epic dep: %v", err)
				}
			}
		}
	}
	builder := &sprint.GraphBuilder{Stories: storiesStore, Dependencies: depsStore}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g, err := builder.Build(ctx, sprint.GraphBuilderOptions{IncludeCompleted: true})
		if err != nil {
			b.Fatalf("Build: %v", err)
		}
		r, _ := sprint.NewRenderer(sprint.FormatDOT, true)
		_ = r.Render(io.Discard, g)
	}
}

// openDBForBench is the *testing.B variant of openDB.
func openDBForBench(b *testing.B) *sqlite.DB {
	b.Helper()
	db, err := sqlite.Open(context.Background(), b.TempDir()+"/bmad-state.db")
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}
