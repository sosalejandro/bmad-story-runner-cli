package sprint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
)

func TestBuildCacheBundle_ConcatenatesPresentFilesAndSkipsMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "arch.md"), []byte("# Architecture body"), 0o644); err != nil {
		t.Fatalf("write arch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.md"), []byte("Audit verdict: ok"), 0o644); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	out := filepath.Join(dir, "bundle.md")

	res, err := sprint.BuildCacheBundle(dir, out, []sprint.CacheBundleSection{
		{Anchor: "architecture.md", Path: "arch.md"},
		{Anchor: "audit.md", Path: "audit.md"},
		{Anchor: "missing.md", Path: "does-not-exist.md"},
	})
	if err != nil {
		t.Fatalf("BuildCacheBundle: %v", err)
	}
	if len(res.IncludedFiles) != 2 {
		t.Errorf("included = %v, want 2", res.IncludedFiles)
	}
	if len(res.MissingFiles) != 1 || res.MissingFiles[0] != "does-not-exist.md" {
		t.Errorf("missing = %v, want [does-not-exist.md]", res.MissingFiles)
	}

	body, _ := os.ReadFile(out)
	got := string(body)
	for _, want := range []string{
		"# BMad v6 sprint cache bundle",
		"## architecture.md",
		"# Architecture body",
		"## audit.md",
		"Audit verdict: ok",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bundle missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestBuildCacheBundle_PrefixIsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("first source"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.md"), []byte("second source"), 0o644)
	sections := []sprint.CacheBundleSection{
		{Anchor: "a", Path: "a.md"},
		{Anchor: "b", Path: "b.md"},
	}

	out1 := filepath.Join(dir, "b1.md")
	out2 := filepath.Join(dir, "b2.md")
	if _, err := sprint.BuildCacheBundle(dir, out1, sections); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if _, err := sprint.BuildCacheBundle(dir, out2, sections); err != nil {
		t.Fatalf("second build: %v", err)
	}
	raw1, _ := os.ReadFile(out1)
	raw2, _ := os.ReadFile(out2)

	// Strip the footer (it contains a timestamp; everything before is the
	// stable prefix Claude's cache could hit on).
	prefix1, _, _ := strings.Cut(string(raw1), "_Generated at")
	prefix2, _, _ := strings.Cut(string(raw2), "_Generated at")
	if prefix1 != prefix2 {
		t.Fatalf("prefix not byte-identical:\n--- run1 ---\n%s\n--- run2 ---\n%s", prefix1, prefix2)
	}
}
