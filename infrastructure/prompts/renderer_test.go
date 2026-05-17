package prompts_test

import (
	"strings"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/prompts"
)

func newRenderer(t *testing.T) *prompts.Renderer {
	t.Helper()
	r, err := prompts.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return r
}

func TestKnown_ListsAllStarterTemplates(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	want := map[string]bool{
		"orchestrator_loop": true,
		"stage_implement":   true,
		"retry_context":     true,
	}
	for _, name := range r.Known() {
		delete(want, name)
	}
	if len(want) > 0 {
		t.Fatalf("Known() missing templates: %v", want)
	}
}

func TestRender_OrchestratorLoop(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	out, err := r.Render("orchestrator_loop", map[string]any{
		"Mode":         "pragmatic",
		"MaxParallel":  4,
		"ReserveRamMB": 8000,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"pragmatic", "4", "8000", "bmad story next", "bmad env up"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRender_StageImplementWithRetry(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	type envCfg struct {
		PgPort, RedisPort, OtelPort int
		DbName                      string
	}
	type prior struct {
		AttemptNo                 int
		PrevStatus, PrevReason    string
		ConcernsCount             int
	}
	out, err := r.Render("stage_implement", map[string]any{
		"StoryID":      "4.1",
		"HydratedFile": "/tmp/4.1.md",
		"Mode":         "strict",
		"EnvConfig":    envCfg{PgPort: 7611, RedisPort: 7612, OtelPort: 7613, DbName: "story_4_1"},
		"PriorAttempt": prior{AttemptNo: 2, PrevStatus: "blocked", PrevReason: "tests failed", ConcernsCount: 1},
		"EpicContext":  "Epic 4 — Identity",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"4.1", "/tmp/4.1.md", "strict", "7611", "story_4_1", "attempt", "tests failed", "Epic 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

func TestRender_UnknownTemplateErrors(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	_, err := r.Render("nope", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown template") {
		t.Fatalf("unknown template err = %v, want 'unknown template'", err)
	}
}

func TestRender_MissingRequiredSlotErrors(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	// Mode missing — text/template missingkey=error should surface this.
	_, err := r.Render("orchestrator_loop", map[string]any{
		"MaxParallel":  4,
		"ReserveRamMB": 8000,
	})
	if err == nil {
		t.Fatalf("expected error on missing required slot, got nil")
	}
}
