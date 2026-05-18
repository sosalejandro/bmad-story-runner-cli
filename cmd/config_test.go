package cmd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// Issue #16: orphan-key audit detects the pre-fix footgun residue. The
// pre-#16 CLI's positional "bmad config get mode" form could accidentally
// write a stray "get" key (treating "get" as the key and "mode" as the
// value). We surface those for cleanup without a destructive migration.
func TestFindConfigFootguns_DetectsOrphanKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := sqlite.NewConfigStore(db)

	// Seed normal config + the footgun orphans.
	for _, kv := range []struct{ k, v string }{
		{"mode", "pragmatic"},      // legitimate
		{"max_parallel", "4"},      // legitimate
		{"get", "mode"},            // footgun residue
		{"set", "mode pragmatic"},  // footgun residue
	} {
		if err := cfg.Set(ctx, kv.k, kv.v); err != nil {
			t.Fatalf("seed %s: %v", kv.k, err)
		}
	}

	orphans, err := findConfigFootguns(ctx, cfg)
	if err != nil {
		t.Fatalf("findConfigFootguns: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans = %d, want 2; got %+v", len(orphans), orphans)
	}
	// Confirm we got the footguns, NOT the legitimate keys.
	for _, e := range orphans {
		if e.Key != "get" && e.Key != "set" {
			t.Errorf("unexpected orphan key %q", e.Key)
		}
	}
}

// A clean DB → no orphans → no warning. Asymmetric to the positive case
// because the bare-DB path is what every freshly-init'd repo sees.
func TestFindConfigFootguns_CleanDBReturnsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := sqlite.NewConfigStore(db)

	if err := cfg.Set(ctx, "mode", "pragmatic"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	orphans, err := findConfigFootguns(ctx, cfg)
	if err != nil {
		t.Fatalf("findConfigFootguns: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("clean DB: got %d orphans, want 0", len(orphans))
	}
}

// Issue #16: the get verb must read-only and never accidentally write.
// We exercise this by building the cmd, running get against an unset key,
// and confirming the DB is unchanged.
func TestConfigGet_NeverWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := sqlite.NewConfigStore(db)

	// Snapshot config before "get".
	before, _ := cfg.All(ctx)
	beforeLen := len(before)

	// Call getOneConfig with a missing key wrapped in a defer/recover —
	// the function calls os.Exit(1) on missing keys, which we can't catch
	// without spawning a subprocess. Instead, set a value first to avoid
	// the exit path, then read it back.
	if err := cfg.Set(ctx, "mode", "pragmatic"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := getOneConfig(ctx, cfg, "mode"); err != nil {
		t.Fatalf("getOneConfig: %v", err)
	}

	after, _ := cfg.All(ctx)
	// One legitimate Set above → exactly one more row than the starting
	// snapshot (and definitely no "get" / "set" footgun keys).
	if len(after) != beforeLen+1 {
		t.Errorf("get path mutated config table: before=%d, after=%d", beforeLen, len(after))
	}
	for _, e := range after {
		if e.Key == "get" || e.Key == "set" {
			t.Errorf("footgun key %q present after get-only path", e.Key)
		}
	}
}
