package state_test

import (
	"context"
	"path/filepath"
	"testing"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func openDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInit_SeedsAllDefaults(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := sqlite.NewConfigStore(db)
	uc := appstate.NewInitUseCase(cfg)

	res, err := uc.Execute(context.Background(), "/some/docs")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.SeededKeys) != len(appstate.DefaultConfig) {
		t.Fatalf("seeded = %d, want %d", len(res.SeededKeys), len(appstate.DefaultConfig))
	}
	if got, _ := cfg.Get(context.Background(), "docs_folder"); got != "/some/docs" {
		t.Fatalf("docs_folder = %q, want /some/docs", got)
	}
}

func TestInit_IsIdempotent(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	cfg := sqlite.NewConfigStore(db)
	uc := appstate.NewInitUseCase(cfg)
	ctx := context.Background()

	_, _ = uc.Execute(ctx, "/a")
	// User tunes a value.
	if err := cfg.Set(ctx, "mode", "strict"); err != nil {
		t.Fatalf("tune: %v", err)
	}

	res, err := uc.Execute(ctx, "/b")
	if err != nil {
		t.Fatalf("re-init: %v", err)
	}
	// All defaults skipped second time around.
	if len(res.SkippedKeys) != len(appstate.DefaultConfig) {
		t.Fatalf("skipped = %d, want %d (all defaults)", len(res.SkippedKeys), len(appstate.DefaultConfig))
	}
	// User-tuned value preserved.
	if got, _ := cfg.Get(ctx, "mode"); got != "strict" {
		t.Fatalf("mode = %q, user tune lost (want strict)", got)
	}
	// docs_folder updated to new value (user-asserted intent on init).
	if got, _ := cfg.Get(ctx, "docs_folder"); got != "/b" {
		t.Fatalf("docs_folder = %q, want /b after re-init", got)
	}
}
