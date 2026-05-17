// Package state defines the v6 state store domain — entity types and narrow
// ports (ISP) that infrastructure adapters satisfy. No persistence concerns
// leak into this package; it is pure data + contracts.
package state

import "time"

// ConfigEntry is a single row in the runtime-knobs config table.
type ConfigEntry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}
