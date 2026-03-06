package domain

type GateResult string

const (
	GatePass     GateResult = "PASS"
	GateFail     GateResult = "FAIL"
	GateConcerns GateResult = "CONCERNS"
)

func ParseGateResult(s string) (GateResult, error) {
	switch GateResult(s) {
	case GatePass, GateFail, GateConcerns:
		return GateResult(s), nil
	default:
		return "", &InvalidGateResultError{Value: s}
	}
}

// IsBlocking returns true if the gate result prevents marking a story complete.
func (g GateResult) IsBlocking() bool {
	return g == GateFail || g == GateConcerns
}

type StoryGate struct {
	StoryID  string
	Result   GateResult
	Concerns []QAConcern
}
