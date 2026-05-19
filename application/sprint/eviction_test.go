package sprint

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/sosalejandro/atlas/packages/shared"
)

// TestEvictOldCacheFiles_KeepsNewestRemovesOldest writes 6 fake cache files
// with staggered mtimes and asserts the eviction policy keeps the 5 newest
// (the issue #12 default budget) and removes the single oldest by mtime.
//
// We exercise the helper directly (white-box internal test) rather than
// routing through BuildCodeContextSection, which would require running the
// real atlas scanner against a synthetic source tree.
func TestEvictOldCacheFiles_KeepsNewestRemovesOldest(t *testing.T) {
	dir := t.TempDir()

	// Six files, oldest first. We pin each file's mtime explicitly so the
	// test isn't subject to FS-timestamp granularity races.
	base := time.Now().Add(-6 * time.Hour)
	heads := []string{
		"aaaaaa1", "bbbbbb2", "cccccc3",
		"dddddd4", "eeeeee5", "ffffff6",
	}
	for i, h := range heads {
		p := filepath.Join(dir, h+".json")
		if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("seed write %s: %v", h, err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", h, err)
		}
	}

	evictOldCacheFiles(dir, 5, shared.NopLogger{})

	got, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	// Oldest (heads[0] = "aaaaaa1") must be gone; the remaining 5 must
	// match heads[1:] exactly.
	want := []string{
		"bbbbbb2.json", "cccccc3.json", "dddddd4.json",
		"eeeeee5.json", "ffffff6.json",
	}
	if len(names) != len(want) {
		t.Fatalf("expected %d files post-eviction, got %d: %v",
			len(want), len(names), names)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("file[%d] = %q, want %q (full list: %v)",
				i, n, want[i], names)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "aaaaaa1.json")); !os.IsNotExist(err) {
		t.Errorf("oldest file aaaaaa1.json should be removed, stat err = %v", err)
	}
}

// TestEvictOldCacheFiles_NoOpWhenUnderCap verifies the helper makes no FS
// changes when sibling count is at or below the cap.
func TestEvictOldCacheFiles_NoOpWhenUnderCap(t *testing.T) {
	dir := t.TempDir()
	for _, h := range []string{"h1.json", "h2.json", "h3.json"} {
		if err := os.WriteFile(filepath.Join(dir, h), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	evictOldCacheFiles(dir, 5, shared.NopLogger{})

	got, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 files (under cap → no-op), got %d", len(got))
	}
}

// TestEvictOldCacheFiles_IgnoresNonJSON ensures stray .tmp / dot-files and
// sub-directories aren't counted toward the budget and aren't removed.
func TestEvictOldCacheFiles_IgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()

	// 5 .json files (at the cap) plus a .tmp staging file and a subdir.
	// Eviction must touch none of them.
	for _, h := range []string{"a.json", "b.json", "c.json", "d.json", "e.json"} {
		if err := os.WriteFile(filepath.Join(dir, h), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("seed json: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "current.json.tmp"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("seed subdir: %v", err)
	}

	evictOldCacheFiles(dir, 5, shared.NopLogger{})

	if _, err := os.Stat(filepath.Join(dir, "current.json.tmp")); err != nil {
		t.Errorf(".tmp staging file removed unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subdir")); err != nil {
		t.Errorf("subdir removed unexpectedly: %v", err)
	}
}

// TestCacheMaxFiles_EnvOverride verifies BMAD_CACHE_MAX_FILES parses
// positive integers and falls back to the default for invalid input.
func TestCacheMaxFiles_EnvOverride(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{"unset_default", "", DefaultCacheMaxFiles},
		{"valid_3", "3", 3},
		{"valid_10", "10", 10},
		{"zero_default", "0", DefaultCacheMaxFiles},
		{"negative_default", "-2", DefaultCacheMaxFiles},
		{"garbage_default", "abc", DefaultCacheMaxFiles},
		{"whitespace_padded", "  4  ", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvCacheMaxFiles, tc.env)
			if got := cacheMaxFiles(); got != tc.want {
				t.Errorf("cacheMaxFiles() = %d, want %d (env=%q)", got, tc.want, tc.env)
			}
		})
	}
}

// TestEvictOldCacheFiles_HonorsCustomCap exercises a non-default cap (2) to
// guard the parameterization — the live path reads it from the env var, but
// the helper itself must obey whatever value it's handed.
func TestEvictOldCacheFiles_HonorsCustomCap(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-10 * time.Hour)
	for i, h := range []string{"o1", "o2", "o3", "o4"} {
		p := filepath.Join(dir, h+".json")
		if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	evictOldCacheFiles(dir, 2, shared.NopLogger{})

	got, _ := os.ReadDir(dir)
	if len(got) != 2 {
		t.Fatalf("expected 2 files after cap=2 eviction, got %d", len(got))
	}
	// Newest two are o3 and o4.
	have := map[string]bool{}
	for _, e := range got {
		have[e.Name()] = true
	}
	if !have["o3.json"] || !have["o4.json"] {
		t.Errorf("expected o3.json + o4.json to survive, got %v", have)
	}
}
