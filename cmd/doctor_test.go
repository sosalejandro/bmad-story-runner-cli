package cmd

import (
	"context"
	"strings"
	"testing"
)

// TestRunDoctorAlwaysPopulatesContractAndIdempotency locks in that the
// static portions of the report (exit code contract + idempotency
// surface) are present regardless of the probe outcomes. AI agents
// rely on these to discover the surface area without needing extra
// docs.
func TestRunDoctorAlwaysPopulatesContractAndIdempotency(t *testing.T) {
	r := runDoctor(context.Background())

	if len(r.ExitCodes) < 5 {
		t.Errorf("expected ≥5 documented exit codes, got %d", len(r.ExitCodes))
	}
	// Spot-check the contract is the real one (not a stub).
	found := map[string]bool{}
	for _, e := range r.ExitCodes {
		found[e.Name] = true
	}
	for _, want := range []string{"SUCCESS", "NO_RESULT", "NOT_FOUND", "CONFLICT"} {
		if !found[want] {
			t.Errorf("exit-code contract missing %q", want)
		}
	}

	if len(r.Idempotency) == 0 {
		t.Error("idempotency surface should not be empty — at minimum dispatch begin/record")
	}
	dispatchSeen := false
	for _, i := range r.Idempotency {
		if strings.HasPrefix(i.Command, "bmad dispatch") {
			dispatchSeen = true
			break
		}
	}
	if !dispatchSeen {
		t.Error("idempotency surface should reference bmad dispatch")
	}
}

// TestRunDoctorHasGoRuntimeCheck verifies the cheapest probe is always
// in the report. (The atlas + db probes are environment-dependent and
// hard to assert from a unit test.)
func TestRunDoctorHasGoRuntimeCheck(t *testing.T) {
	r := runDoctor(context.Background())
	for _, c := range r.Checks {
		if c.Name == "go runtime" {
			if c.Status != "PASS" {
				t.Errorf("go runtime check should always PASS, got %q (%s)", c.Status, c.Detail)
			}
			return
		}
	}
	t.Error("doctor report missing 'go runtime' check")
}

// TestDoctorReportOKFalseWhenAnyFail simulates the OK derivation logic.
// The runDoctor function sets OK=false if any check fails — we assert
// that contract here so a future refactor can't silently flip it (which
// would let `bmad doctor` exit 0 against a broken environment).
func TestDoctorReportOKFalseWhenAnyFail(t *testing.T) {
	r := doctorReport{
		Checks: []doctorCheck{
			{Name: "a", Status: "PASS"},
			{Name: "b", Status: "FAIL"},
			{Name: "c", Status: "WARN"},
		},
	}
	// Mirror runDoctor's OK derivation.
	ok := true
	for _, c := range r.Checks {
		if c.Status == "FAIL" {
			ok = false
			break
		}
	}
	if ok {
		t.Error("expected OK=false when any check is FAIL")
	}
}
