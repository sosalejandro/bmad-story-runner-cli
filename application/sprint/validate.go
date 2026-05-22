package sprint

import (
	"fmt"
	"sort"
	"strings"
)

// ---------- Public API ----------

// FindingKind enumerates the validator's diagnostic categories. Stable; new
// kinds must be additive — orchestrators key off these strings.
type FindingKind string

const (
	FindingCycle             FindingKind = "cycle"
	FindingOrphan            FindingKind = "orphan"
	FindingMissingDep        FindingKind = "missing_dep"
	FindingMissingEpicRef    FindingKind = "missing_epic_ref"
	FindingPlaceholderSmell  FindingKind = "placeholder_smell"
	FindingDiamond           FindingKind = "diamond"
)

// FindingSeverity drives the exit-code mapping.
//
//	error → caller exits VALIDATION_ERROR (30)
//	warn  → exit 0 unless --strict is set
//	info  → exit 0 always (advisory only)
type FindingSeverity string

const (
	SeverityError FindingSeverity = "error"
	SeverityWarn  FindingSeverity = "warn"
	SeverityInfo  FindingSeverity = "info"
)

// Finding is one validator diagnostic. Field shape is the v1 wire contract
// — additive evolution only.
type Finding struct {
	Kind        FindingKind     `json:"kind"`
	Severity    FindingSeverity `json:"severity"`
	InvolvedIDs []string        `json:"involved_ids"`
	Message     string          `json:"message"`
	// SuggestedFix is an optional, human-readable nudge — populated for
	// placeholder_smell findings where we can point at the architecture
	// doc the operator should consult.
	SuggestedFix string `json:"suggested_fix,omitempty"`
}

// Counts is the per-severity tally rendered alongside Findings.
type Counts struct {
	Error int `json:"error"`
	Warn  int `json:"warn"`
	Info  int `json:"info"`
}

// ValidateReport is the result envelope's .result body.
type ValidateReport struct {
	Findings []Finding `json:"findings"`
	Counts   Counts    `json:"counts"`
	// Scope, when non-empty, echoes the --scope filter applied before the
	// graph was walked.
	Scope string `json:"scope,omitempty"`
	// Strict echoes whether warn-level findings were escalated to errors
	// for the exit-code decision.
	Strict bool `json:"strict"`
	// TotalStories is the count of stories considered by the validator
	// (after scope filtering). Surfaced so consumers can sanity-check.
	TotalStories int `json:"total_stories"`
}

// ValidateOptions parameterises a Validate call. Kept as a struct (not
// positional args) so additive fields don't break callers.
type ValidateOptions struct {
	// Scope, when non-empty, restricts the validator to stories whose ID
	// belongs to this epic id (see StoryMatchesEpic). Cross-epic edges
	// pointing OUT of scope are still treated as missing-dep candidates
	// when the target is absent from the input slice — same shape as
	// `bmad sprint plan` partial-coverage rules.
	Scope string

	// Strict, when true, escalates orphan findings (default WARN) to
	// errors so the caller exits non-zero. Other severities are unchanged.
	Strict bool

	// EpicRequires maps "epic id" → list of declared upstream epic ids
	// it depends on (the future #46 `requires_epics:` field at the epic
	// header). Used by the placeholder-smell detector to SUPPRESS the
	// finding when "first story of Epic N depends_on last story of Epic
	// N-1" matches an explicit `requires_epics: [N-1]` declaration.
	//
	// Nil map = no declarations parsed; every "linear-chain" pattern
	// will be flagged. Once #46 lands, the cobra wrapper populates this
	// from epic-level frontmatter.
	EpicRequires map[string][]string
}

// Validate runs every detector over the parsed stories and returns a
// deterministic Report (findings sorted by kind then by first involved id).
//
// The function is pure: no IO, no DB. Inputs in → findings out. Tests
// inject ParsedStory fixtures directly; the cobra wrapper parses
// epics.md first.
func Validate(parsed []ParsedStory, opts ValidateOptions) ValidateReport {
	scoped := parsed
	if opts.Scope != "" {
		scoped = FilterByScope(parsed, opts.Scope)
	}

	// Build a graph view of the scoped set. byID is the authoritative
	// "this story exists in the input" check.
	byID := make(map[string]ParsedStory, len(scoped))
	order := make([]string, 0, len(scoped))
	for _, ps := range scoped {
		id := ps.Frontmatter.StoryID
		if _, dup := byID[id]; !dup {
			order = append(order, id)
		}
		byID[id] = ps
	}

	var findings []Finding
	findings = append(findings, detectMissingDeps(byID, order)...)
	findings = append(findings, detectCycles(byID, order)...)
	findings = append(findings, detectOrphans(byID, order)...)
	findings = append(findings, detectPlaceholderSmell(byID, order, opts.EpicRequires)...)
	findings = append(findings, detectDiamonds(byID, order)...)

	// Deterministic ordering: kind alpha, then involved_ids[0] alpha.
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		a, b := first(findings[i].InvolvedIDs), first(findings[j].InvolvedIDs)
		return a < b
	})

	rep := ValidateReport{
		Findings:     findings,
		Strict:       opts.Strict,
		Scope:        opts.Scope,
		TotalStories: len(scoped),
	}
	for _, f := range findings {
		switch f.Severity {
		case SeverityError:
			rep.Counts.Error++
		case SeverityWarn:
			rep.Counts.Warn++
		case SeverityInfo:
			rep.Counts.Info++
		}
	}
	return rep
}

// HasBlockingFindings returns true if the report contains any severity that
// must map to a non-zero exit code, given the strict flag.
//
//	Strict=false → only errors block.
//	Strict=true  → errors + warns both block.
//
// Info-level never blocks.
func (r ValidateReport) HasBlockingFindings() bool {
	if r.Counts.Error > 0 {
		return true
	}
	if r.Strict && r.Counts.Warn > 0 {
		return true
	}
	return false
}

// ---------- Detectors ----------

// detectMissingDeps flags `depends_on` entries pointing to a story id that
// is not present in the parsed set. Severity = error (graph is broken).
//
// Out-of-scope handling: when scope is restricted, missing-dep can be a
// false positive if the target lives in a different epic. We don't have
// enough information here to distinguish "external done dep" from "typo"
// — by convention the validator runs against the FULL epics.md file
// (scope filters the start set, but the byID lookup is built from the
// scoped slice). Operators who want to validate a single epic in
// isolation can pre-filter; otherwise they should validate the whole
// file. We make this explicit in the message text.
func detectMissingDeps(byID map[string]ParsedStory, order []string) []Finding {
	var out []Finding
	for _, id := range order {
		ps := byID[id]
		for _, dep := range ps.Frontmatter.DependsOn {
			if dep == "" {
				continue
			}
			if _, ok := byID[dep]; ok {
				continue
			}
			out = append(out, Finding{
				Kind:        FindingMissingDep,
				Severity:    SeverityError,
				InvolvedIDs: []string{id, dep},
				Message: fmt.Sprintf(
					"Story %s depends_on non-existent story %q (not present in input set)",
					id, dep),
			})
		}
	}
	return out
}

// detectCycles runs an iterative three-color DFS over the dependency
// graph. We use the iterative form to keep the call stack bounded on
// deep dep chains, and we report ONE finding per cycle (the canonical
// closed walk from the back-edge target back to itself).
//
// The graph direction we walk: edge A → B means "A depends_on B"
// (A waits for B). A cycle is therefore a closed walk where each step
// is a depends_on transition.
func detectCycles(byID map[string]ParsedStory, order []string) []Finding {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(order))
	parent := make(map[string]string, len(order))

	var cycles [][]string
	seen := map[string]bool{} // dedupe by canonical cycle key

	var visit func(start string)
	visit = func(start string) {
		// Iterative DFS with an explicit (node, iter-index) stack so we
		// can detect back-edges + reconstruct the cycle.
		type frame struct {
			node string
			next int // index into the next dep to visit
		}
		stack := []frame{{node: start, next: 0}}
		color[start] = gray
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			ps := byID[top.node]
			deps := ps.Frontmatter.DependsOn
			if top.next >= len(deps) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			dep := deps[top.next]
			top.next++
			if dep == "" {
				continue
			}
			if _, ok := byID[dep]; !ok {
				continue // missing-dep — reported by its own detector
			}
			switch color[dep] {
			case white:
				parent[dep] = top.node
				color[dep] = gray
				stack = append(stack, frame{node: dep, next: 0})
			case gray:
				// Back-edge: reconstruct cycle dep → ... → top.node → dep.
				cycle := []string{dep}
				cur := top.node
				for cur != dep && cur != "" {
					cycle = append(cycle, cur)
					cur = parent[cur]
				}
				cycle = append(cycle, dep)
				// reverse so the walk reads in dependency-order:
				// "dep → ... → top.node → dep". The slice above was
				// built from back-edge target walking BACK through
				// parents, so we reverse to present forward.
				reverseStrings(cycle)
				key := cycleKey(cycle)
				if !seen[key] {
					seen[key] = true
					cycles = append(cycles, cycle)
				}
			}
		}
	}

	for _, id := range order {
		if color[id] == white {
			visit(id)
		}
	}

	out := make([]Finding, 0, len(cycles))
	for _, c := range cycles {
		out = append(out, Finding{
			Kind:        FindingCycle,
			Severity:    SeverityError,
			InvolvedIDs: c,
			Message:     "Cycle: " + strings.Join(c, " -> "),
		})
	}
	return out
}

// reverseStrings reverses s in place.
func reverseStrings(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// cycleKey canonicalises a cycle path for dedup so the same cycle
// discovered from two different DFS entry points only reports once.
// We rotate the slice so the smallest id is first, then join.
func cycleKey(cycle []string) string {
	if len(cycle) < 2 {
		return strings.Join(cycle, "|")
	}
	// drop trailing duplicate of the start (we render closed walks with
	// the start id appearing twice — strip one for canonicalisation).
	body := cycle[:len(cycle)-1]
	minIdx := 0
	for i := 1; i < len(body); i++ {
		if body[i] < body[minIdx] {
			minIdx = i
		}
	}
	rot := append([]string{}, body[minIdx:]...)
	rot = append(rot, body[:minIdx]...)
	return strings.Join(rot, "|")
}

// detectOrphans flags stories that have no dependents (no other story
// in the set has them in `depends_on`) AND are not the last story of
// their epic.
//
// "Last story of epic" is determined positionally: the story with the
// highest numeric suffix within the epic id prefix. We use a numeric
// compare when the suffix parses as an integer; otherwise lexicographic.
// This matches the convention used elsewhere in the planner.
//
// Severity = warn (it's often a typo, but legitimate side-quest stories
// can have no dependents — e.g. a one-off doc fix in the middle of an
// epic). --strict escalates these to errors.
func detectOrphans(byID map[string]ParsedStory, order []string) []Finding {
	// dependents[id] = set of stories that depend on id
	dependents := make(map[string]map[string]bool, len(order))
	for _, id := range order {
		ps := byID[id]
		for _, dep := range ps.Frontmatter.DependsOn {
			if dep == "" {
				continue
			}
			if _, ok := byID[dep]; !ok {
				continue
			}
			if dependents[dep] == nil {
				dependents[dep] = map[string]bool{}
			}
			dependents[dep][id] = true
		}
	}

	// Group story ids by epic.
	byEpic := make(map[string][]string)
	for _, id := range order {
		epic := EpicIDFromStory(id)
		byEpic[epic] = append(byEpic[epic], id)
	}
	lastOfEpic := make(map[string]string)
	for epic, ids := range byEpic {
		last := pickLastByStorySuffix(ids)
		lastOfEpic[epic] = last
	}

	var out []Finding
	for _, id := range order {
		if len(dependents[id]) > 0 {
			continue
		}
		epic := EpicIDFromStory(id)
		if lastOfEpic[epic] == id {
			continue
		}
		out = append(out, Finding{
			Kind:        FindingOrphan,
			Severity:    SeverityWarn,
			InvolvedIDs: []string{id},
			Message: fmt.Sprintf(
				"Story %s has no successors and is not the last story of Epic %s — likely a missing depends_on linkage elsewhere",
				id, epic),
		})
	}
	return out
}

// pickLastByStorySuffix returns the story id with the largest suffix
// after the first dot. Numeric compare when possible, else lexicographic.
// Empty input returns "".
func pickLastByStorySuffix(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	best := ids[0]
	for _, id := range ids[1:] {
		if compareStorySuffix(id, best) > 0 {
			best = id
		}
	}
	return best
}

// compareStorySuffix orders two story ids by their post-first-dot suffix.
// Returns >0 if a > b, <0 if a < b, 0 if equal.
//
// Strategy: take everything after the first dot; if both suffixes parse
// as ints, compare numerically; otherwise lex. This handles "1.10 > 1.9"
// correctly (numeric) while still ordering "4.1.payment > 4.1.identity"
// lexicographically.
func compareStorySuffix(a, b string) int {
	sa := suffixAfterFirstDot(a)
	sb := suffixAfterFirstDot(b)
	if ia, ok1 := parseStoryNumber(sa); ok1 {
		if ib, ok2 := parseStoryNumber(sb); ok2 {
			switch {
			case ia > ib:
				return 1
			case ia < ib:
				return -1
			default:
				return 0
			}
		}
	}
	switch {
	case sa > sb:
		return 1
	case sa < sb:
		return -1
	default:
		return 0
	}
}

func suffixAfterFirstDot(id string) string {
	if i := strings.IndexByte(id, '.'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// parseStoryNumber returns (n, true) only when s is a pure unsigned int.
func parseStoryNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// detectPlaceholderSmell flags the "first story of Epic N depends_on last
// story of Epic N-1, with no epic-level requires_epics: [N-1] declared".
// This is a heuristic — it intentionally errs on the side of surfacing
// noise the operator can then clear, because the cost of a missed real
// cross-epic dep is much higher than the cost of an extra INFO line.
//
// Suppression: when EpicRequires[N] contains "N-1", we skip the finding
// (the operator has explicitly declared the cross-epic relationship at
// the epic level, so the linear-chain frontmatter is intentional, not
// a placeholder).
//
// Defensive code path for #46: if EpicRequires is nil OR lacks an entry
// for Epic N, we emit the finding. Once #46 lands and the cobra wrapper
// populates the map from epic-level frontmatter, declarations will start
// suppressing matches without any change to the detector.
func detectPlaceholderSmell(byID map[string]ParsedStory, order []string, epicRequires map[string][]string) []Finding {
	// Group ids by epic.
	byEpic := make(map[string][]string)
	for _, id := range order {
		byEpic[EpicIDFromStory(id)] = append(byEpic[EpicIDFromStory(id)], id)
	}

	// firstOfEpic[epic] = lowest-suffix story in epic.
	firstOfEpic := make(map[string]string, len(byEpic))
	lastOfEpic := make(map[string]string, len(byEpic))
	for epic, ids := range byEpic {
		first := ids[0]
		last := ids[0]
		for _, id := range ids[1:] {
			if compareStorySuffix(id, first) < 0 {
				first = id
			}
			if compareStorySuffix(id, last) > 0 {
				last = id
			}
		}
		firstOfEpic[epic] = first
		lastOfEpic[epic] = last
	}

	var out []Finding
	for _, id := range order {
		epic := EpicIDFromStory(id)
		if firstOfEpic[epic] != id {
			continue // only first-of-epic candidates can smell
		}
		ps := byID[id]
		// Need exactly one depends_on entry, and it must be the last
		// story of a numerically-adjacent earlier epic.
		if len(ps.Frontmatter.DependsOn) != 1 {
			continue
		}
		dep := ps.Frontmatter.DependsOn[0]
		depEpic := EpicIDFromStory(dep)
		if depEpic == epic {
			continue // same-epic intra-link is normal, not a smell
		}
		if !isPreviousEpicNumeric(epic, depEpic) {
			continue
		}
		if lastOfEpic[depEpic] != dep {
			continue
		}
		// Suppression — explicit requires_epics declaration.
		if declared := epicRequires[epic]; containsString(declared, depEpic) {
			continue
		}
		out = append(out, Finding{
			Kind:        FindingPlaceholderSmell,
			Severity:    SeverityInfo,
			InvolvedIDs: []string{id, dep},
			Message: fmt.Sprintf(
				"Story %s is the first of Epic %s and only depends on %s (last of Epic %s) — likely a linear-chain placeholder rather than a real semantic dep",
				id, epic, dep, depEpic),
			SuggestedFix: fmt.Sprintf(
				"Either declare an epic-level `requires_epics: [%q]` at the Epic %s header (see docs/architecture/eda-cutover/saga-to-slice-mapping*.md) OR replace this depends_on with the specific story(ies) Story %s actually consumes from.",
				depEpic, epic, id),
		})
	}
	return out
}

// isPreviousEpicNumeric returns true when both epic and prevCandidate
// parse as ints and prevCandidate == epic - 1. Non-numeric epic ids
// (e.g. slug-style "plans-patient") never trigger the smell — we lack
// a notion of "previous" for them.
func isPreviousEpicNumeric(epic, prevCandidate string) bool {
	a, ok1 := parseStoryNumber(epic)
	b, ok2 := parseStoryNumber(prevCandidate)
	if !ok1 || !ok2 {
		return false
	}
	return b == a-1
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// detectDiamonds flags ids that have ≥2 distinct dependency-paths
// converging on the same ancestor. A diamond is sometimes legit
// (a synchronisation point where one story really does consume two
// upstreams that share a common predecessor) — severity is therefore
// INFO. The motivation is observability: surfacing diamonds tells the
// orchestrator's batch planner that the converging story will block
// behind whichever path finishes last.
//
// Detection: for each story S, walk its transitive ancestor set via
// BFS counting visit-paths (NOT just visited nodes). Any ancestor with
// >1 visit-path through S's direct deps is a diamond apex.
func detectDiamonds(byID map[string]ParsedStory, order []string) []Finding {
	// Pre-compute transitive ancestors per direct-dep so we can attribute
	// shared ancestors.
	out := []Finding{}
	for _, id := range order {
		ps := byID[id]
		directDeps := uniqueExisting(ps.Frontmatter.DependsOn, byID)
		if len(directDeps) < 2 {
			continue
		}
		// Per-direct-dep reachable set.
		reachByDep := make(map[string]map[string]bool, len(directDeps))
		for _, dep := range directDeps {
			reachByDep[dep] = ancestorsOf(dep, byID)
		}
		// An ancestor that's reachable through ≥2 distinct directDeps is
		// a diamond apex.
		counts := map[string]int{}
		for _, dep := range directDeps {
			for anc := range reachByDep[dep] {
				counts[anc]++
			}
		}
		var apexes []string
		for anc, n := range counts {
			if n >= 2 {
				apexes = append(apexes, anc)
			}
		}
		if len(apexes) == 0 {
			continue
		}
		sort.Strings(apexes)
		// Emit one finding per (story, apex) pair so consumers can group
		// findings cleanly.
		for _, apex := range apexes {
			out = append(out, Finding{
				Kind:        FindingDiamond,
				Severity:    SeverityInfo,
				InvolvedIDs: []string{id, apex},
				Message: fmt.Sprintf(
					"Story %s has multiple dependency paths converging on %s (diamond) — review whether the convergence is intentional",
					id, apex),
			})
		}
	}
	return out
}

// uniqueExisting filters a depends_on slice down to ids actually present
// in the parsed set, deduping while preserving first-seen order. Used
// by diamond detection so a duplicate edge (e.g. depends_on: ["a", "a"])
// doesn't masquerade as two-path convergence.
func uniqueExisting(deps []string, byID map[string]ParsedStory) []string {
	seen := make(map[string]bool, len(deps))
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		if d == "" {
			continue
		}
		if _, ok := byID[d]; !ok {
			continue
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// ancestorsOf returns the transitive ancestor set of start (excluding
// start itself). Used by diamond detection. Iterative BFS to stay
// bounded on deep chains.
func ancestorsOf(start string, byID map[string]ParsedStory) map[string]bool {
	out := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		ps := byID[cur]
		for _, dep := range ps.Frontmatter.DependsOn {
			if dep == "" {
				continue
			}
			if _, ok := byID[dep]; !ok {
				continue
			}
			if out[dep] {
				continue
			}
			out[dep] = true
			queue = append(queue, dep)
		}
	}
	return out
}

// first returns the head of a slice or "" when empty. Used for ordering.
func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
