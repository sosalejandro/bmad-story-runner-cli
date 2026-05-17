package state

import "context"

// Config is the narrow port for the runtime-knobs config table (spec §7).
// Implementations are infrastructure adapters (e.g., sqlite). Consumers
// depend on this interface, never on the adapter type.
type Config interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	All(ctx context.Context) ([]ConfigEntry, error)
	Delete(ctx context.Context, key string) error
}
