package state

import "context"

// Depguard is the per-rule warn→error flip port (spec §13 / golangci-depguard
// hardening). Flip writes both the current-state row and an audit-log event;
// history queries return the chronological flip events for a rule.
type Depguard interface {
	Get(ctx context.Context, rule string) (DepguardFlip, error)
	All(ctx context.Context) ([]DepguardFlip, error)
	Flip(ctx context.Context, rule string, to DepguardState, reason string) error
	History(ctx context.Context, rule string) ([]DepguardFlipEvent, error)
}
