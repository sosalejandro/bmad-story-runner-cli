package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// emitConfigGetJSON powers `bmad config get <key> --json`. Returns the
// key + value under .result; mirrors the human-mode behaviour of
// exiting 1 on a missing key, but emits a warning-bearing envelope to
// stdout first so a downstream JQ chain still sees the input it ran on.
func emitConfigGetJSON(ctx context.Context, c *cobra.Command, cfg state.Config, key string) error {
	v, err := cfg.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		_ = emitJSONStdout(commandPathSansRoot(c),
			map[string]any{"key": key},
			map[string]any{"key": key, "value": nil},
			[]string{fmt.Sprintf("config: key %q not set", key)},
		)
		fmt.Fprintf(os.Stderr, "config: key %q not set\n", key)
		os.Exit(1)
	}
	if err != nil {
		return err
	}
	return emitJSONStdout(commandPathSansRoot(c),
		map[string]any{"key": key},
		map[string]any{"key": key, "value": v}, nil)
}

// emitConfigSetJSON powers `bmad config set <key> <value> --json`. We
// read the previous value first so the envelope can report it under
// .result.previous_value — useful for audits + JQ-driven diffs in the
// orchestrator's state-change logs. A missing previous key is reported
// as null, NOT the empty string, so consumers can disambiguate "absent"
// from "explicitly empty".
func emitConfigSetJSON(ctx context.Context, c *cobra.Command, cfg state.Config, key, value string) error {
	var previous any
	if prev, err := cfg.Get(ctx, key); err == nil {
		previous = prev
	} // ErrNotFound → leave previous as nil
	if err := cfg.Set(ctx, key, value); err != nil {
		return err
	}
	return emitJSONStdout(commandPathSansRoot(c),
		map[string]any{"key": key, "value": value},
		map[string]any{
			"ok":             true,
			"key":            key,
			"value":          value,
			"previous_value": previous,
		}, nil)
}

// orphanRowJSON describes one config-audit orphan-key row under --json.
// Fields mirror state.ConfigEntry but as JSON-stable types (strings, not
// time.Time, for forward compat with consumers that parse loosely).
type orphanRowJSON struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// emitConfigAuditJSON powers `bmad config audit --json`. Returns an
// object with `orphans` (array, possibly empty) + `count` (int) so a
// downstream check can `.result.count > 0` without inspecting the array
// directly.
func emitConfigAuditJSON(c *cobra.Command, orphans []state.ConfigEntry) error {
	rows := make([]orphanRowJSON, 0, len(orphans))
	for _, e := range orphans {
		rows = append(rows, orphanRowJSON{
			Key:       e.Key,
			Value:     e.Value,
			UpdatedAt: e.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return emitJSONStdout(commandPathSansRoot(c), nil,
		map[string]any{
			"count":   len(rows),
			"orphans": rows,
		}, nil)
}
