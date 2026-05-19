package prompts_test

import (
	"regexp"
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

// ─────────────────────────────────────────────────────────────────────────────
// TOKEN_BREAKDOWN instruction (issue #20)
//
// Every stage_*.tmpl must include the token-breakdown reporting partial in
// its suffix so the L3 agent emits a parseable usage line as the last line
// of its response. The L1 orchestrator's parser regex is the contract.

// tokenBreakdownAnchor is the literal anchor the L1 orchestrator's parser
// matches against. Must stay in sync with the partial template
// `infrastructure/prompts/templates/token_breakdown_instruction.tmpl`.
const tokenBreakdownAnchor = "TOKEN_BREAKDOWN: input=N output=N cache_read=N cache_create=N"

// tokenBreakdownParseRegex is the exact regex shape the orchestrator parses
// off the L3 agent's response. Matches `^TOKEN_BREAKDOWN: input=\d+ output=\d+ cache_read=\d+ cache_create=\d+`.
// This test only validates the regex compiles + matches a synthetic line —
// the real consumer is the orchestrator skill in nutrition develop.
var tokenBreakdownParseRegex = regexp.MustCompile(`(?m)^TOKEN_BREAKDOWN: input=(\d+) output=(\d+) cache_read=(\d+) cache_create=(\d+)\s*$`)

func TestRender_TokenBreakdownInstruction_PresentInEveryStage(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)

	envCommon := map[string]any{"PgPort": 7611, "RedisPort": 7612, "OtelPort": 7613, "DbName": "story_x"}
	cases := []struct {
		stage string
		data  map[string]any
	}{
		{"stage_hydrate", map[string]any{
			"StoryID": "1.1", "Mode": "pragmatic",
			"EpicsPath": "/p/epics.md", "ArchitecturePath": "/p/arch.md",
			"HydratedFile": "/p/1.1.md",
		}},
		{"stage_atdd", map[string]any{
			"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "strict",
			"EnvConfig": envCommon,
		}},
		{"stage_automate", map[string]any{
			"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "strict",
			"EnvConfig": envCommon,
		}},
		{"stage_implement", map[string]any{
			"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic",
			"EnvConfig": envCommon,
		}},
		{"stage_test_review", map[string]any{
			"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "strict",
			"EnvConfig": envCommon,
		}},
		{"stage_code_review", map[string]any{
			"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic",
			"Iteration": 1, "MaxIterations": 3, "EnvConfig": envCommon,
		}},
		{"stage_commit", map[string]any{
			"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic",
			"Branch": "fix/x",
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.stage, func(t *testing.T) {
			t.Parallel()
			out, err := r.Render(c.stage, c.data)
			if err != nil {
				t.Fatalf("render %s: %v", c.stage, err)
			}
			if !strings.Contains(out, tokenBreakdownAnchor) {
				t.Fatalf("%s: rendered prompt missing TOKEN_BREAKDOWN instruction anchor %q\n--- output ---\n%s",
					c.stage, tokenBreakdownAnchor, out)
			}
			// The instruction must live in the SUFFIX, not the PREFIX —
			// the partial is shared text but its placement must be below
			// the cache boundary so an unbundled prompt-cache prefix
			// still benefits from anything above the marker.
			if idxMarker := strings.Index(out, stageCacheBoundaryMarker); idxMarker >= 0 {
				idxAnchor := strings.Index(out, tokenBreakdownAnchor)
				if idxAnchor < idxMarker {
					t.Fatalf("%s: TOKEN_BREAKDOWN anchor appears BEFORE cache boundary — instruction must be in the suffix to preserve prefix invariance",
						c.stage)
				}
			}
		})
	}
}

func TestTokenBreakdownParseRegex_MatchesSyntheticAgentLine(t *testing.T) {
	t.Parallel()
	// Sanity: the regex shape the orchestrator parser uses must actually
	// match a well-formed line. Pins the contract so a future template
	// edit can't silently rename a field without breaking this test.
	cases := []struct {
		name string
		line string
		want [4]string // input, output, cache_read, cache_create
	}{
		{"happy path", "TOKEN_BREAKDOWN: input=12345 output=678 cache_read=9000 cache_create=4321", [4]string{"12345", "678", "9000", "4321"}},
		{"all zeros", "TOKEN_BREAKDOWN: input=0 output=0 cache_read=0 cache_create=0", [4]string{"0", "0", "0", "0"}},
		{"trailing newline", "TOKEN_BREAKDOWN: input=1 output=2 cache_read=3 cache_create=4\n", [4]string{"1", "2", "3", "4"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := tokenBreakdownParseRegex.FindStringSubmatch(c.line)
			if m == nil {
				t.Fatalf("no match for %q", c.line)
			}
			got := [4]string{m[1], m[2], m[3], m[4]}
			if got != c.want {
				t.Fatalf("submatch mismatch: got %v want %v", got, c.want)
			}
		})
	}

	// Negative cases — must NOT match.
	neg := []string{
		"TOKEN_BREAKDOWN: input=unknown output=0 cache_read=0 cache_create=0",
		"TOKEN_BREAKDOWN input=1 output=2 cache_read=3 cache_create=4", // missing colon
		"token_breakdown: input=1 output=2 cache_read=3 cache_create=4", // wrong case
		"prefix TOKEN_BREAKDOWN: input=1 output=2 cache_read=3 cache_create=4", // not anchored start-of-line
	}
	for _, s := range neg {
		if tokenBreakdownParseRegex.FindString(s) != "" {
			t.Errorf("regex unexpectedly matched %q", s)
		}
	}
}

// The TOKEN_BREAKDOWN instruction adds STATIC text — it must not bust the
// cache-invariance contract for any stage. Re-runs the cross-story prefix
// check against a stage to confirm the partial doesn't introduce per-call
// drift.
func TestRender_TokenBreakdownInstruction_DoesNotBustCacheInvariance(t *testing.T) {
	t.Parallel()
	r := newRenderer(t)
	for _, stage := range []string{"stage_implement", "stage_hydrate", "stage_code_review", "stage_commit"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			a := map[string]any{
				"StoryID": "1.1", "HydratedFile": "/p/1.1.md", "Mode": "pragmatic",
				"EnvConfig": map[string]any{"PgPort": 7611, "DbName": "story_a"},
				"EpicsPath": "/p/epics.md", "ArchitecturePath": "/p/arch.md",
				"Branch":    "fix/x", "Iteration": 1, "MaxIterations": 3,
			}
			b := map[string]any{
				"StoryID": "9.9", "HydratedFile": "/q/9.9.md", "Mode": "strict",
				"EnvConfig": map[string]any{"PgPort": 7801, "DbName": "story_b"},
				"EpicsPath": "/q/epics.md", "ArchitecturePath": "/q/arch.md",
				"Branch":    "fix/y", "Iteration": 2, "MaxIterations": 3,
			}
			ra, err := r.Render(stage, a)
			if err != nil {
				t.Fatalf("render A (%s): %v", stage, err)
			}
			rb, err := r.Render(stage, b)
			if err != nil {
				t.Fatalf("render B (%s): %v", stage, err)
			}
			if extractStablePrefix(t, ra) != extractStablePrefix(t, rb) {
				t.Fatalf("%s: prefix diverged after TOKEN_BREAKDOWN partial was added", stage)
			}
			// Both renders must contain the instruction (defense-in-depth).
			if !strings.Contains(ra, tokenBreakdownAnchor) || !strings.Contains(rb, tokenBreakdownAnchor) {
				t.Errorf("%s: TOKEN_BREAKDOWN instruction missing from one of the renders", stage)
			}
		})
	}
}
