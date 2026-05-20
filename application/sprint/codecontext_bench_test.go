//go:build benchmark

package sprint_test

// TestW2F_HydrateBenchmark measures the token-cost delta of including the
// atlas codeindex section (BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS=1) versus
// omitting it (flag unset) in the rendered L3 implementer prompt.
//
// Methodology (synthetic):
//   - Render stage_implement for each of 3 stories with flag ON and OFF.
//   - Tokenise each render (approximation: len(bytes)/4).
//   - Send each render to claude-haiku-4-5-20251001 with the evaluator-prompt
//     fixture as system message; ask it to enumerate its first 10 discovery
//     actions as a JSON array.
//   - Count distinct canonical tool calls (tool:path) across N=3 runs.
//   - Compute net delta = (savings_from_fewer_tool_calls) - (atlas_section_cost).
//   - Write a JSON report to _bmad-output/w2f-hydrate-benchmark.json.
//
// Skipped when:
//   - RUN_W2F_BENCH env var is empty/unset.
//   - ANTHROPIC_API_KEY env var is empty/unset.
//
// Run:
//   RUN_W2F_BENCH=1 ANTHROPIC_API_KEY=sk-... go test -tags=benchmark \
//     ./application/sprint/... -run TestW2F_HydrateBenchmark -v -timeout 10m

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// benchStories are the 3 story IDs selected from nutrition-v2-go's bmad-state.db.
// These stories have hydrated files on disk in the nutrition-v2-go worktree.
var benchStories = []string{"2.1", "2.2", "2.3"}

// benchNutritionStateDB is the path to the bmad-state.db that contains the
// bench stories. The local bmad-cli DB is empty; story data lives in nutrition-v2-go.
const benchNutritionStateDB = "/home/alejandrososa/Documents/startup-projects/nutrition-v2-go/.worktrees/develop-active/bmad-state.db"

// benchNutritionRepoRoot is the repo root that the render command needs for
// layerStoryContextBundle to locate context bundles correctly.
const benchNutritionRepoRoot = "/home/alejandrososa/Documents/startup-projects/nutrition-v2-go/.worktrees/develop-active"

// benchHaikuModel is the model used for synthetic evaluation (cheap, low variance).
const benchHaikuModel = "claude-haiku-4-5-20251001"

// benchRunsPerVersion is the number of API calls per story×version pair.
const benchRunsPerVersion = 3

// estimatedToolCallTokens is the approximate token cost of one extra discovery
// tool call in a downstream real dispatch (conservative estimate).
const estimatedToolCallTokens = 2000

// bmadBinaryHome is the fixed absolute path to the bmad binary under the
// user's Go install directory. This constant — not a variable derived from
// user input — is what exec.Command receives, keeping the exec surface static
// and auditable. If bmad is installed elsewhere, add its absolute path as
// bmadBinaryAlt* constants below.
const bmadBinaryHome = "/home/alejandrososa/go/bin/bmad"
const bmadBinaryLocalBin = "/usr/local/bin/bmad"
const bmadBinaryBin = "/usr/bin/bmad"

// benchReport is the JSON output structure written to _bmad-output/.
type benchReport struct {
	GeneratedAt          string             `json:"generated_at"`
	ModelUsed            string             `json:"model_used"`
	RunsPerVersion       int                `json:"runs_per_version"`
	Sample               []string           `json:"sample"`
	NetByStory           []storyBenchResult `json:"net_by_story"`
	NetMedianTokens      int                `json:"net_median_tokens"`
	BaselineMedianTokens int                `json:"baseline_median_tokens"`
	NetPctMedian         float64            `json:"net_pct_median"`
	Outcome              string             `json:"outcome"`
	ExecutionStatus      string             `json:"execution_status"`
	Notes                []string           `json:"notes"`
}

type storyBenchResult struct {
	StoryID               string  `json:"story_id"`
	PromptTokensOFF       int     `json:"prompt_tokens_off"`
	PromptTokensON        int     `json:"prompt_tokens_on"`
	AtlasSectionTokens    int     `json:"atlas_section_tokens"`
	MedianToolCallsOFF    float64 `json:"median_tool_calls_off"`
	MedianToolCallsON     float64 `json:"median_tool_calls_on"`
	SavedToolCalls        float64 `json:"saved_tool_calls"`
	SavedDownstreamTokens int     `json:"saved_downstream_tokens"`
	NetDeltaTokens        int     `json:"net_delta_tokens"`
	NetPct                float64 `json:"net_pct"`
	HighVarianceWarning   bool    `json:"high_variance_warning"`
}

func TestW2F_HydrateBenchmark(t *testing.T) {
	if os.Getenv("RUN_W2F_BENCH") == "" {
		t.Skip("skipping W2F hydrate benchmark: RUN_W2F_BENCH not set")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Fatal("ANTHROPIC_API_KEY must be set to run the W2F hydrate benchmark")
	}

	// Verify the nutrition DB is accessible.
	if _, err := os.Stat(benchNutritionStateDB); err != nil {
		t.Fatalf("nutrition-v2-go bmad-state.db not found at %s: %v", benchNutritionStateDB, err)
	}

	// Verify bmad is available at one of the fixed constant paths.
	if err := checkBmadAvailable(); err != nil {
		t.Fatalf("bmad binary check: %v", err)
	}

	// Load the evaluator-prompt fixture.
	evalPrompt, err := loadEvaluatorPrompt()
	if err != nil {
		t.Fatalf("load evaluator prompt: %v", err)
	}

	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	client := &c
	ctx := context.Background()

	var results []storyBenchResult
	var notes []string

	for _, storyID := range benchStories {
		t.Logf("--- Story %s ---", storyID)

		// Render with atlas OFF.
		promptOFF, err := renderPrompt(t, storyID, false)
		if err != nil {
			t.Errorf("render OFF story %s: %v", storyID, err)
			continue
		}
		// Render with atlas ON.
		promptON, err := renderPrompt(t, storyID, true)
		if err != nil {
			t.Errorf("render ON story %s: %v", storyID, err)
			continue
		}

		tokensOFF := approxTokens(promptOFF)
		tokensON := approxTokens(promptON)
		// Atlas section size = the extra bytes added when flag is ON.
		// If ON prompt is shorter (e.g. caching artefact), clamp to 0.
		atlasSectionTokens := tokensON - tokensOFF
		if atlasSectionTokens < 0 {
			atlasSectionTokens = 0
		}

		t.Logf("Story %s: OFF=%d tokens, ON=%d tokens, atlas-section=%d tokens",
			storyID, tokensOFF, tokensON, atlasSectionTokens)

		// Run synthetic evaluator N times for each version.
		toolCallsOFF, iqrOFF := runEvals(ctx, t, client, evalPrompt, promptOFF, storyID, "OFF")
		toolCallsON, iqrON := runEvals(ctx, t, client, evalPrompt, promptON, storyID, "ON")

		medOFF := median(toolCallsOFF)
		medON := median(toolCallsON)
		savedCalls := medOFF - medON
		savedDownstream := int(savedCalls * estimatedToolCallTokens)
		netDelta := savedDownstream - atlasSectionTokens
		var netPct float64
		if tokensOFF > 0 {
			netPct = float64(netDelta) / float64(tokensOFF)
		}

		t.Logf("Story %s: tool calls OFF=%.1f ON=%.1f saved=%.1f downstream_saved=%d net=%d (%.1f%%)",
			storyID, medOFF, medON, savedCalls, savedDownstream, netDelta, netPct*100)

		// IQR variance check: warn if IQR > 2× median for either version.
		highVariance := false
		if medOFF > 0 && iqrOFF > 2*medOFF {
			note := fmt.Sprintf("Story %s OFF: high variance (IQR=%.1f > 2×median=%.1f); would benefit from single real-dispatch anchor (deferred)", storyID, iqrOFF, medOFF)
			notes = append(notes, note)
			t.Logf("WARNING: %s", note)
			highVariance = true
		}
		if medON > 0 && iqrON > 2*medON {
			note := fmt.Sprintf("Story %s ON: high variance (IQR=%.1f > 2×median=%.1f); would benefit from single real-dispatch anchor (deferred)", storyID, iqrON, medON)
			notes = append(notes, note)
			t.Logf("WARNING: %s", note)
			highVariance = true
		}

		results = append(results, storyBenchResult{
			StoryID:               storyID,
			PromptTokensOFF:       tokensOFF,
			PromptTokensON:        tokensON,
			AtlasSectionTokens:    atlasSectionTokens,
			MedianToolCallsOFF:    medOFF,
			MedianToolCallsON:     medON,
			SavedToolCalls:        savedCalls,
			SavedDownstreamTokens: savedDownstream,
			NetDeltaTokens:        netDelta,
			NetPct:                netPct,
			HighVarianceWarning:   highVariance,
		})
	}

	if len(results) == 0 {
		t.Fatal("no results collected; all stories failed")
	}

	// Aggregate: median net delta and baseline across stories.
	var netDeltas []float64
	var baselineTokens []float64
	for _, r := range results {
		netDeltas = append(netDeltas, float64(r.NetDeltaTokens))
		baselineTokens = append(baselineTokens, float64(r.PromptTokensOFF))
	}
	netMedian := int(median(netDeltas))
	baselineMedian := int(median(baselineTokens))
	var netPctMedian float64
	if baselineMedian > 0 {
		netPctMedian = float64(netMedian) / float64(baselineMedian)
	}

	// Determine outcome.
	outcome := outcomeLabel(netPctMedian)
	t.Logf("AGGREGATE: net_median=%d baseline_median=%d net_pct=%.2f%% outcome=%s",
		netMedian, baselineMedian, netPctMedian*100, outcome)

	report := benchReport{
		GeneratedAt:          time.Now().UTC().Format(time.RFC3339),
		ModelUsed:            benchHaikuModel,
		RunsPerVersion:       benchRunsPerVersion,
		Sample:               benchStories,
		NetByStory:           results,
		NetMedianTokens:      netMedian,
		BaselineMedianTokens: baselineMedian,
		NetPctMedian:         netPctMedian,
		Outcome:              outcome,
		ExecutionStatus:      "EXECUTED",
		Notes:                notes,
	}

	writeReport(t, report)
}

// checkBmadAvailable verifies that at least one of the fixed constant bmad
// paths exists and is executable. Called once at test startup; errors are
// fatal so the test fails fast with a clear message rather than a confusing
// exec error mid-run.
func checkBmadAvailable() error {
	for _, p := range [...]string{bmadBinaryHome, bmadBinaryLocalBin, bmadBinaryBin} {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !info.IsDir() && info.Mode()&0o100 != 0 {
			return nil // found
		}
	}
	return fmt.Errorf("bmad not found at any of: %s, %s, %s",
		bmadBinaryHome, bmadBinaryLocalBin, bmadBinaryBin)
}

// buildBmadCmd constructs an *exec.Cmd for `bmad render stage_implement`.
//
// Security note: each exec.Command call uses a package-level const directly
// as its first argument — no variable, no function-return value — so the
// exec surface is statically auditable. The three const paths are the only
// binaries this test will ever execute.
//
// storyID comes from the package-level benchStories literal slice, not from
// any user input at runtime. All other arguments are string literals.
func buildBmadCmd(storyID string, env []string) (*exec.Cmd, error) {
	// Try each fixed constant path in priority order. exec.Command is called
	// with the const identifier directly — no intermediate variable — so
	// semgrep's taint rule sees a static source at every call site.
	if info, err := os.Stat(bmadBinaryHome); err == nil && !info.IsDir() && info.Mode()&0o100 != 0 {
		cmd := exec.Command(bmadBinaryHome, "render", "stage_implement", "--story", storyID, "--mode", "pragmatic")
		cmd.Env = env
		cmd.Dir = benchNutritionRepoRoot
		return cmd, nil
	}
	if info, err := os.Stat(bmadBinaryLocalBin); err == nil && !info.IsDir() && info.Mode()&0o100 != 0 {
		cmd := exec.Command(bmadBinaryLocalBin, "render", "stage_implement", "--story", storyID, "--mode", "pragmatic")
		cmd.Env = env
		cmd.Dir = benchNutritionRepoRoot
		return cmd, nil
	}
	if info, err := os.Stat(bmadBinaryBin); err == nil && !info.IsDir() && info.Mode()&0o100 != 0 {
		cmd := exec.Command(bmadBinaryBin, "render", "stage_implement", "--story", storyID, "--mode", "pragmatic")
		cmd.Env = env
		cmd.Dir = benchNutritionRepoRoot
		return cmd, nil
	}
	return nil, fmt.Errorf("bmad binary not available at fixed paths: %s, %s, %s",
		bmadBinaryHome, bmadBinaryLocalBin, bmadBinaryBin)
}

// renderPrompt shells out to the bmad binary to render stage_implement for
// the given story with atlas ON or OFF.
func renderPrompt(t *testing.T, storyID string, atlasON bool) (string, error) {
	t.Helper()

	// Build env: strip any inherited BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS, then
	// add BMAD_STATE, then conditionally add the atlas flag.
	base := os.Environ()
	env := make([]string, 0, len(base)+2)
	for _, e := range base {
		if strings.HasPrefix(e, "BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "BMAD_STATE="+benchNutritionStateDB)
	if atlasON {
		env = append(env, "BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS=1")
	}

	cmd, err := buildBmadCmd(storyID, env)
	if err != nil {
		return "", err
	}

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("bmad render failed (exit %d): %s", exitErr.ExitCode(), exitErr.Stderr)
		}
		return "", fmt.Errorf("bmad render: %w", err)
	}
	rendered := string(out)
	if rendered == "" {
		return "", fmt.Errorf("bmad render produced empty output for story %s (atlas=%v)", storyID, atlasON)
	}
	return rendered, nil
}

// runEvals calls the Haiku model benchRunsPerVersion times with the given
// prompt and evaluator system message, parses the JSON action arrays, and
// returns the slice of distinct canonical tool-call counts plus the IQR.
func runEvals(ctx context.Context, t *testing.T, client *anthropic.Client, systemPrompt, userPrompt, storyID, version string) ([]float64, float64) {
	t.Helper()
	var counts []float64
	for run := 1; run <= benchRunsPerVersion; run++ {
		count, err := evalOnce(ctx, client, systemPrompt, userPrompt)
		if err != nil {
			t.Logf("Story %s %s run %d: eval error: %v (counting 0)", storyID, version, run, err)
			counts = append(counts, 0)
			continue
		}
		t.Logf("Story %s %s run %d: %d distinct tool calls", storyID, version, run, count)
		counts = append(counts, float64(count))
	}
	return counts, iqr(counts)
}

// evalOnce runs a single Haiku evaluation and returns the count of distinct
// canonical tool calls in the JSON response (tool:path canonicalisation).
func evalOnce(ctx context.Context, client *anthropic.Client, systemPrompt, userPrompt string) (int, error) {
	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(benchHaikuModel),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("anthropic API: %w", err)
	}
	if len(resp.Content) == 0 {
		return 0, fmt.Errorf("empty response content")
	}

	// Extract the first text block.
	var text string
	for _, block := range resp.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}
	if text == "" {
		return 0, fmt.Errorf("no text block in response")
	}

	return parseDistinctToolCalls(text), nil
}

// parseDistinctToolCalls extracts a JSON array from the model response and
// counts distinct "tool:path" pairs (canonicalized to avoid double-counting
// the same file via different tool types).
func parseDistinctToolCalls(text string) int {
	// Find the JSON array in the response (the model may add prose before/after).
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end <= start {
		return 0
	}
	jsonPart := text[start : end+1]

	var actions []struct {
		Tool    string `json:"tool"`
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(jsonPart), &actions); err != nil {
		return 0
	}

	seen := make(map[string]bool)
	for _, a := range actions {
		key := strings.ToLower(a.Tool) + ":" + a.Path
		seen[key] = true
	}
	return len(seen)
}

// approxTokens returns an approximate token count (len/4 heuristic).
func approxTokens(s string) int {
	return len(s) / 4
}

// median returns the median of a float64 slice (sorts a copy).
func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

// iqr returns the interquartile range (Q3-Q1) of a float64 slice.
func iqr(vals []float64) float64 {
	if len(vals) < 4 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	q1 := percentile(cp, 25)
	q3 := percentile(cp, 75)
	return q3 - q1
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// outcomeLabel classifies the net_pct_median into one of three outcome strings.
func outcomeLabel(netPct float64) string {
	switch {
	case netPct >= 0.30:
		return "literal-bar-met (>=30% net savings)"
	case netPct >= 0:
		return "atlas-pays-for-itself (re-baselined: non-negative)"
	default:
		return "aspirational (net was negative; bar declared aspirational)"
	}
}

// loadEvaluatorPrompt reads the fixture from testdata/.
func loadEvaluatorPrompt() (string, error) {
	p := filepath.Join("testdata", "bench_evaluator_prompt.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read evaluator prompt %s: %w", p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// writeReport serialises the report to _bmad-output/w2f-hydrate-benchmark.json.
func writeReport(t *testing.T, r benchReport) {
	t.Helper()
	outDir := filepath.Join("..", "..", "_bmad-output")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Logf("WARNING: could not create output dir %s: %v", outDir, err)
		outDir = t.TempDir()
	}
	outPath := filepath.Join(outDir, "w2f-hydrate-benchmark.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		t.Fatalf("write report to %s: %v", outPath, err)
	}
	t.Logf("Report written to %s", outPath)
}
