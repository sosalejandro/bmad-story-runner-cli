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

// Issue #17: a doc-only story (no port allocation) must render cleanly
// without an EnvConfig, and the resulting prompt must NOT mention
// Postgres / Redis / OTEL.
func TestRender_StageImplement_NoEnvConfig_OmitsTestEnvBlock(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	out, err := r.Render("stage_implement", map[string]any{
		"StoryID":      "1.1",
		"HydratedFile": "/tmp/1.1.md",
		"Mode":         "pragmatic",
		// Deliberately omitting EnvConfig — seedOptionalSlots fills the
		// zero-port map so the conditional collapses the env block.
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, mustNot := range []string{
		"Test environment",
		"PostgreSQL",
		"Postgres",
		"Redis",
		"OTEL",
	} {
		if strings.Contains(out, mustNot) {
			t.Errorf("doc-only prompt still mentions %q\n--- output ---\n%s", mustNot, out)
		}
	}
	// Core blocks still present.
	for _, want := range []string{"Implement", "Story ID: **1.1**", "/tmp/1.1.md", "pragmatic"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// Issue #17: a feature story (full EnvConfig) must STILL emit the test
// environment block — the change is backwards-compatible for the existing
// dispatch path.
func TestRender_StageImplement_FullEnvConfig_KeepsTestEnvBlock(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	out, err := r.Render("stage_implement", map[string]any{
		"StoryID":      "4.1",
		"HydratedFile": "/tmp/4.1.md",
		"Mode":         "strict",
		"EnvConfig": map[string]any{
			"PgPort":    7611,
			"RedisPort": 7612,
			"OtelPort":  7613,
			"DbName":    "story_4_1",
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"Test environment",
		"PostgreSQL",
		"7611",
		"story_4_1",
		"Redis",
		"7612",
		"OTEL",
		"7613",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("feature-story prompt missing %q", want)
		}
	}
}

// Issue #17 corner case: a story with Postgres only (no Redis / OTEL) —
// we must NOT emit empty Redis/OTEL bullets, and the heading must remain.
func TestRender_StageImplement_PgOnly_OmitsRedisAndOtel(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	out, err := r.Render("stage_implement", map[string]any{
		"StoryID":      "5.1",
		"HydratedFile": "/tmp/5.1.md",
		"Mode":         "pragmatic",
		"EnvConfig": map[string]any{
			"PgPort": 7801,
			"DbName": "story_5_1",
			// RedisPort, OtelPort intentionally unset.
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Test environment") {
		t.Errorf("Test environment heading missing\n%s", out)
	}
	if !strings.Contains(out, "7801") {
		t.Errorf("Pg port missing")
	}
	if strings.Contains(out, "Redis") {
		t.Errorf("Redis bullet appeared without a port\n%s", out)
	}
	if strings.Contains(out, "OTEL") {
		t.Errorf("OTEL bullet appeared without a port\n%s", out)
	}
}

// Issue #20: cache-invariance contract.
//
// Anthropic's prompt cache requires a byte-identical prefix across dispatches
// for cache_read_input_tokens > 0. Epic 1 + Epic 2 batch 1 baseline saw 0%
// cache hits across 1.25M input tokens because the templates put per-story
// variable data (StoryID, HydratedFile, EnvConfig) at the TOP — the cache
// boundary broke at line 1.
//
// The restructure (this PR) moves the stable per-stage content (protocol,
// atlas guide, return-JSON schema) to a PREFIX, marked off with the literal
// cache-boundary comment marker, and pushes per-story slots into a SUFFIX
// under "## Story dispatch".
//
// These tests lock in the invariance: rendering the same stage with two
// different stories must produce IDENTICAL bytes up through the cache
// boundary marker, then diverge.

// stageCacheBoundaryMarker is the heading every stage_*.tmpl uses to open
// its per-story SUFFIX. The marker is anchored on `\n## Story dispatch\n`
// (start-of-line + end-of-line) so prose mentions of the section name
// inside the prefix don't false-match. This must stay in sync with
// cmd/render.go's cacheBoundaryMarker constant.
const stageCacheBoundaryMarker = "\n## Story dispatch\n"

func extractStablePrefix(t *testing.T, rendered string) string {
	t.Helper()
	idx := strings.Index(rendered, stageCacheBoundaryMarker)
	if idx < 0 {
		t.Fatalf("rendered output missing cache-boundary marker %q — template not restructured\n---\n%s",
			stageCacheBoundaryMarker, rendered)
	}
	// Include the leading newline so the prefix ends naturally.
	return rendered[:idx+1]
}

func TestRender_CacheInvariance_StageImplement_PrefixStableAcrossStories(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	a, err := r.Render("stage_implement", map[string]any{
		"StoryID":      "1.1",
		"HydratedFile": "/abs/path/to/1.1.md",
		"Mode":         "pragmatic",
		"EnvConfig": map[string]any{
			"PgPort": 7611, "RedisPort": 7612, "OtelPort": 7613, "DbName": "story_1_1",
		},
		"IdempotencyKey": "key-aaaa-1111",
	})
	if err != nil {
		t.Fatalf("render A: %v", err)
	}
	b, err := r.Render("stage_implement", map[string]any{
		"StoryID":      "4.7",
		"HydratedFile": "/different/path/4.7.md",
		"Mode":         "strict",
		"EnvConfig": map[string]any{
			"PgPort": 7801, "RedisPort": 7802, "OtelPort": 7803, "DbName": "story_4_7",
		},
		"IdempotencyKey": "key-bbbb-2222",
	})
	if err != nil {
		t.Fatalf("render B: %v", err)
	}
	prefixA := extractStablePrefix(t, a)
	prefixB := extractStablePrefix(t, b)
	if prefixA != prefixB {
		t.Fatalf("stage_implement prefix diverged across stories — cache invariance broken\n--- A ---\n%s\n--- B ---\n%s",
			prefixA, prefixB)
	}
	// And the SUFFIXES must actually differ — otherwise we accidentally
	// moved the story-id out of the rendered output entirely.
	if !strings.Contains(a, "1.1") || !strings.Contains(b, "4.7") {
		t.Errorf("story ids missing from suffix — rendered output dropped the variable data")
	}
}

func TestRender_CacheInvariance_AllStages_PrefixStableAcrossStories(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)

	// Two distinct slot-data shapes per stage. The variable bits must
	// shift; everything before the cache-boundary marker must hold.
	type slotPair struct {
		stage string
		a, b  map[string]any
	}
	envA := map[string]any{"PgPort": 7611, "RedisPort": 7612, "OtelPort": 7613, "DbName": "story_a"}
	envB := map[string]any{"PgPort": 7801, "RedisPort": 7802, "OtelPort": 7803, "DbName": "story_b"}

	pairs := []slotPair{
		{
			stage: "stage_atdd",
			a: map[string]any{"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "strict", "EnvConfig": envA, "IdempotencyKey": "k1"},
			b: map[string]any{"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "strict", "EnvConfig": envB, "IdempotencyKey": "k2"},
		},
		{
			stage: "stage_automate",
			a: map[string]any{"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "strict", "EnvConfig": envA, "IdempotencyKey": "k1"},
			b: map[string]any{"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "strict", "EnvConfig": envB, "IdempotencyKey": "k2"},
		},
		{
			stage: "stage_test_review",
			a: map[string]any{"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "strict", "EnvConfig": envA, "IdempotencyKey": "k1"},
			b: map[string]any{"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "strict", "EnvConfig": envB, "IdempotencyKey": "k2"},
		},
		{
			stage: "stage_code_review",
			a: map[string]any{"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic", "Iteration": 1, "MaxIterations": 3, "EnvConfig": envA, "IdempotencyKey": "k1"},
			b: map[string]any{"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "pragmatic", "Iteration": 2, "MaxIterations": 3, "EnvConfig": envB, "IdempotencyKey": "k2"},
		},
		{
			stage: "stage_commit",
			a: map[string]any{"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic", "Branch": "fix/x-1", "IdempotencyKey": "k1"},
			b: map[string]any{"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "pragmatic", "Branch": "fix/y-2", "IdempotencyKey": "k2"},
		},
		{
			stage: "stage_hydrate",
			a: map[string]any{
				"StoryID": "1.1", "Mode": "pragmatic",
				"EpicsPath": "/p/epics.md", "ArchitecturePath": "/p/arch.md",
				"HydratedFile": "/p/1.1.md", "IdempotencyKey": "k1",
			},
			b: map[string]any{
				"StoryID": "9.9", "Mode": "strict",
				"EpicsPath": "/q/epics.md", "ArchitecturePath": "/q/arch.md",
				"HydratedFile": "/q/9.9.md", "IdempotencyKey": "k2",
			},
		},
		{
			stage: "stage_implement",
			a: map[string]any{"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic", "EnvConfig": envA, "IdempotencyKey": "k1"},
			b: map[string]any{"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "strict", "EnvConfig": envB, "IdempotencyKey": "k2"},
		},
	}
	for _, p := range pairs {
		p := p
		t.Run(p.stage, func(t *testing.T) {
			t.Parallel()
			ra, err := r.Render(p.stage, p.a)
			if err != nil {
				t.Fatalf("render A (%s): %v", p.stage, err)
			}
			rb, err := r.Render(p.stage, p.b)
			if err != nil {
				t.Fatalf("render B (%s): %v", p.stage, err)
			}
			prefixA := extractStablePrefix(t, ra)
			prefixB := extractStablePrefix(t, rb)
			if prefixA != prefixB {
				t.Fatalf("%s prefix differs across two distinct stories — cache invariance broken\n--- A ---\n%s\n--- B ---\n%s",
					p.stage, prefixA, prefixB)
			}
		})
	}
}

// Cache invariance must hold across mode changes for stages that don't
// vary protocol by mode (everything except code-review which has separate
// strict/pragmatic protocol blocks — but those BOTH live in the prefix,
// so a mode flip still keeps the prefix byte-identical).
func TestRender_CacheInvariance_ModeFlip_PrefixStable(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	common := func(mode string) map[string]any {
		return map[string]any{
			"StoryID":      "1.1",
			"HydratedFile": "/p/1.1.md",
			"Mode":         mode,
			"EnvConfig":    map[string]any{"PgPort": 7611, "DbName": "story_x"},
		}
	}
	for _, stage := range []string{"stage_implement", "stage_code_review"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			dataA := common("pragmatic")
			dataB := common("strict")
			if stage == "stage_code_review" {
				dataA["Iteration"] = 1
				dataA["MaxIterations"] = 3
				dataB["Iteration"] = 1
				dataB["MaxIterations"] = 3
			}
			ra, err := r.Render(stage, dataA)
			if err != nil {
				t.Fatalf("render pragmatic: %v", err)
			}
			rb, err := r.Render(stage, dataB)
			if err != nil {
				t.Fatalf("render strict: %v", err)
			}
			if extractStablePrefix(t, ra) != extractStablePrefix(t, rb) {
				t.Fatalf("%s: prefix changed when only mode flipped — protocol blocks must both live in the prefix",
					stage)
			}
		})
	}
}

// PriorAttempt is variable per dispatch (attempt number, prior reason) —
// it MUST live in the suffix, not the prefix. A retry of the same story
// should still re-use the prefix from the first attempt.
func TestRender_CacheInvariance_PriorAttempt_DoesNotBustPrefix(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	base := map[string]any{
		"StoryID":      "1.1",
		"HydratedFile": "/p/1.1.md",
		"Mode":         "pragmatic",
		"EnvConfig":    map[string]any{"PgPort": 7611, "DbName": "story_x"},
	}
	withRetry := map[string]any{
		"StoryID":      "1.1",
		"HydratedFile": "/p/1.1.md",
		"Mode":         "pragmatic",
		"EnvConfig":    map[string]any{"PgPort": 7611, "DbName": "story_x"},
		"PriorAttempt": map[string]any{
			"AttemptNo": 2, "PrevStatus": "blocked", "PrevReason": "tests failed", "ConcernsCount": 1,
		},
	}
	a, err := r.Render("stage_implement", base)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}
	b, err := r.Render("stage_implement", withRetry)
	if err != nil {
		t.Fatalf("render retry: %v", err)
	}
	if extractStablePrefix(t, a) != extractStablePrefix(t, b) {
		t.Fatalf("prefix changed when PriorAttempt was added — retry context must live in suffix")
	}
}
