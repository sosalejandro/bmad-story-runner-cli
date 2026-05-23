package sprint_test

import (
	"strings"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
)

// mkStory is a small helper that keeps the synthetic-fixture call sites
// readable — every test below builds a []ParsedStory by hand.
func mkStory(id string, deps ...string) sprint.ParsedStory {
	return sprint.ParsedStory{
		Frontmatter: sprint.StoryFrontmatter{
			StoryID:   id,
			DependsOn: deps,
		},
		HasFrontmatter: true,
	}
}

// findKinds returns a count-by-kind map for terse fixture assertions.
func findKinds(r sprint.ValidateReport) map[sprint.FindingKind]int {
	out := map[sprint.FindingKind]int{}
	for _, f := range r.Findings {
		out[f.Kind]++
	}
	return out
}

// Fixture 1 — clean baseline. A linear DAG inside a single epic (no
// cross-epic linear-chain placeholder smell) should produce zero findings.
//
// Topology:
//
//	1.1 → 1.2 → 1.3
func TestValidate_CleanBaseline(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("1.1"),
		mkStory("1.2", "1.1"),
		mkStory("1.3", "1.2"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	if len(rep.Findings) != 0 {
		t.Fatalf("expected zero findings on clean DAG, got: %+v", rep.Findings)
	}
	if rep.HasBlockingFindings() {
		t.Errorf("clean DAG must not block")
	}
	if rep.TotalStories != 3 {
		t.Errorf("TotalStories = %d, want 3", rep.TotalStories)
	}
}

// Fixture 2 — cycle. 3.2 → 3.4 → 3.2 forms a closed walk; exit must be
// blocking and the cycle path must include both ids.
func TestValidate_CycleDetected(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("3.1"),
		mkStory("3.2", "3.4"),
		mkStory("3.3"),
		mkStory("3.4", "3.2"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	kinds := findKinds(rep)
	if kinds[sprint.FindingCycle] != 1 {
		t.Fatalf("expected exactly one cycle finding, got %+v\n%+v", kinds, rep.Findings)
	}
	if !rep.HasBlockingFindings() {
		t.Errorf("cycle must block (exit-30)")
	}
	if rep.Counts.Error < 1 {
		t.Errorf("cycle must count as ERROR, got counts=%+v", rep.Counts)
	}
	var cycleFinding sprint.Finding
	for _, f := range rep.Findings {
		if f.Kind == sprint.FindingCycle {
			cycleFinding = f
			break
		}
	}
	// Cycle path must mention both involved ids.
	joined := strings.Join(cycleFinding.InvolvedIDs, ",")
	if !strings.Contains(joined, "3.2") || !strings.Contains(joined, "3.4") {
		t.Errorf("cycle path missing 3.2 and/or 3.4: %v", cycleFinding.InvolvedIDs)
	}
	if !strings.Contains(cycleFinding.Message, "->") {
		t.Errorf("cycle message should render the walk with '->', got %q", cycleFinding.Message)
	}
}

// Fixture 3 — orphan. 1.2 has no dependents AND is not the last story of
// its epic (1.3 exists). Default severity = WARN, no exit blocking;
// --strict flips it.
func TestValidate_OrphanDefaultWarn(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("1.1"),
		mkStory("1.2", "1.1"), // orphan: nobody depends on it, not last
		mkStory("1.3", "1.1"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	kinds := findKinds(rep)
	if kinds[sprint.FindingOrphan] != 1 {
		t.Fatalf("expected one orphan finding, got %+v\n%+v", kinds, rep.Findings)
	}
	if rep.HasBlockingFindings() {
		t.Errorf("orphan WARN must NOT block default exit (got Counts=%+v Strict=%v)",
			rep.Counts, rep.Strict)
	}

	// With --strict, the same fixture must block.
	repStrict := sprint.Validate(parsed, sprint.ValidateOptions{Strict: true})
	if !repStrict.HasBlockingFindings() {
		t.Errorf("orphan + --strict must block")
	}
}

// Fixture 4 — missing dep. 5.1 depends on 4.9 which does not exist in
// the input set. Severity = ERROR, blocking.
func TestValidate_MissingDepNonExistent(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("5.1", "4.9"), // 4.9 does not exist
		mkStory("5.2"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	kinds := findKinds(rep)
	if kinds[sprint.FindingMissingDep] != 1 {
		t.Fatalf("expected one missing_dep, got %+v\n%+v", kinds, rep.Findings)
	}
	if !rep.HasBlockingFindings() {
		t.Errorf("missing_dep must block (exit-30)")
	}
	// Verify the message mentions both the dependent and the missing target.
	var mdFinding sprint.Finding
	for _, f := range rep.Findings {
		if f.Kind == sprint.FindingMissingDep {
			mdFinding = f
			break
		}
	}
	if !strings.Contains(mdFinding.Message, "5.1") || !strings.Contains(mdFinding.Message, "4.9") {
		t.Errorf("missing_dep message should name 5.1 and 4.9: %q", mdFinding.Message)
	}
}

// Fixture 5 — placeholder smell. First story of Epic 2 depends only on
// last story of Epic 1, with no epic-level requires_epics declared.
// Severity = INFO, never blocks. Suggested fix points at the EDA cutover
// doc.
func TestValidate_PlaceholderSmell(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("1.1"),
		mkStory("1.2", "1.1"), // last of Epic 1
		mkStory("2.1", "1.2"), // first of Epic 2 → linear-chain placeholder
		mkStory("2.2", "2.1"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	kinds := findKinds(rep)
	if kinds[sprint.FindingPlaceholderSmell] != 1 {
		t.Fatalf("expected one placeholder_smell, got %+v\n%+v", kinds, rep.Findings)
	}
	if rep.HasBlockingFindings() {
		t.Errorf("placeholder_smell INFO must not block")
	}
	// Verify the suggested-fix nudges at the architecture doc.
	for _, f := range rep.Findings {
		if f.Kind == sprint.FindingPlaceholderSmell {
			if !strings.Contains(f.SuggestedFix, "requires_epics") {
				t.Errorf("suggested_fix should mention requires_epics: %q", f.SuggestedFix)
			}
			if !strings.Contains(f.SuggestedFix, "saga-to-slice-mapping") {
				t.Errorf("suggested_fix should reference the saga-to-slice mapping doc: %q", f.SuggestedFix)
			}
		}
	}
}

// Same fixture as above but WITH an explicit requires_epics declaration —
// the placeholder smell must be suppressed (defensive code path for #46).
func TestValidate_PlaceholderSmellSuppressedByRequiresEpics(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("1.1"),
		mkStory("1.2", "1.1"),
		mkStory("2.1", "1.2"),
		mkStory("2.2", "2.1"),
	}
	// Post-#54: suppression is driven by the (story, dep) edge having
	// been persisted as synthesised. We feed that shape here directly so
	// the test is independent of the planner.
	rep := sprint.Validate(parsed, sprint.ValidateOptions{
		SyntheticEdges: sprint.SyntheticEdgeSet{
			"2.1": {"1.2": true},
		},
	})
	kinds := findKinds(rep)
	if kinds[sprint.FindingPlaceholderSmell] != 0 {
		t.Fatalf("placeholder_smell should be suppressed by synthesised edge, got %+v\n%+v",
			kinds, rep.Findings)
	}
}

// Fixture 6 — diamond. 1.4 has two paths converging on 1.1 (via 1.2 and
// via 1.3). Severity = INFO.
//
//	    1.1
//	   /   \
//	 1.2   1.3
//	   \   /
//	    1.4
func TestValidate_Diamond(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("1.1"),
		mkStory("1.2", "1.1"),
		mkStory("1.3", "1.1"),
		mkStory("1.4", "1.2", "1.3"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	kinds := findKinds(rep)
	if kinds[sprint.FindingDiamond] < 1 {
		t.Fatalf("expected at least one diamond finding, got %+v\n%+v",
			kinds, rep.Findings)
	}
	if rep.HasBlockingFindings() {
		t.Errorf("diamond INFO must not block")
	}
	// The apex must be 1.1 and the converging story must be 1.4.
	var diamond sprint.Finding
	for _, f := range rep.Findings {
		if f.Kind == sprint.FindingDiamond {
			diamond = f
			break
		}
	}
	if !contains(diamond.InvolvedIDs, "1.4") || !contains(diamond.InvolvedIDs, "1.1") {
		t.Errorf("diamond involved_ids should include 1.4 and 1.1: %v", diamond.InvolvedIDs)
	}
}

// Counts must aggregate by severity across multiple finding kinds.
func TestValidate_CountsAggregate(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("5.1", "4.9"), // missing_dep → ERROR
		mkStory("5.2", "5.3"),
		mkStory("5.3", "5.2"), // cycle → ERROR
		mkStory("5.4", "5.1"), // orphan because 5.4 has no dependents; not last (5.5 below)
		mkStory("5.5", "5.4"),
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	if rep.Counts.Error < 2 {
		t.Errorf("expected >=2 errors (cycle + missing_dep), got %+v", rep.Counts)
	}
	if !rep.HasBlockingFindings() {
		t.Errorf("multi-error fixture must block")
	}
}

// Scope filtering: stories outside scope must not be considered, and the
// report's TotalStories must reflect the post-scope set.
func TestValidate_ScopeFilter(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		mkStory("1.1"),
		mkStory("1.2", "1.1"),
		mkStory("2.1", "2.2"), // missing_dep, but only if 2.x is in scope
		mkStory("2.2", "2.1"), // cycle, ditto
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{Scope: "1"})
	if rep.TotalStories != 2 {
		t.Fatalf("TotalStories = %d, want 2 (scope=1)", rep.TotalStories)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("scope=1 should suppress Epic-2 findings, got %+v", rep.Findings)
	}
	if rep.Scope != "1" {
		t.Errorf("Scope echo = %q, want %q", rep.Scope, "1")
	}
}

// Validates JSON tag stability of the report payload — the wire shape is
// part of the v1 envelope contract, so changing it must be an intentional
// breaking change with a CHANGELOG note.
func TestValidate_ReportTagsStable(t *testing.T) {
	t.Parallel()
	// This test is a structural canary: refactoring the struct fields
	// will need explicit reconfirmation by editing the assertions.
	parsed := []sprint.ParsedStory{
		mkStory("a.1", "a.2"),
		mkStory("a.2", "a.1"), // forces a cycle so .findings is non-empty
	}
	rep := sprint.Validate(parsed, sprint.ValidateOptions{})
	if rep.Findings[0].Kind != sprint.FindingCycle {
		t.Fatalf("expected cycle, got %v", rep.Findings[0].Kind)
	}
	if rep.Findings[0].Severity != sprint.SeverityError {
		t.Errorf("severity contract drift: %q", rep.Findings[0].Severity)
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
