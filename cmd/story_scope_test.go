package cmd

import (
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// TestFilterStoriesByScope covers the helper backing `bmad story status
// --scope`. Mirrors the appsprint.StoryMatchesEpic prefix+dot rule.
func TestFilterStoriesByScope(t *testing.T) {
	rows := []state.Story{
		{ID: "1.1"}, {ID: "1.2"},
		{ID: "2.1"}, {ID: "2.2"}, {ID: "2.3"},
		{ID: "10.1"},
	}

	tests := []struct {
		scope string
		want  []string
	}{
		{"", []string{"1.1", "1.2", "2.1", "2.2", "2.3", "10.1"}},
		{"1", []string{"1.1", "1.2"}}, // must NOT match 10.1
		{"2", []string{"2.1", "2.2", "2.3"}},
		{"10", []string{"10.1"}},
		{"99", []string{}}, // no match → empty
	}

	for _, tc := range tests {
		// filterStoriesByScope mutates its input via rows[:0] reslicing, so
		// hand it a fresh copy per case.
		input := make([]state.Story, len(rows))
		copy(input, rows)
		got := filterStoriesByScope(input, tc.scope)
		if len(got) != len(tc.want) {
			t.Fatalf("scope %q: len = %d, want %d (got=%+v)", tc.scope, len(got), len(tc.want), got)
		}
		for i, st := range got {
			if st.ID != tc.want[i] {
				t.Fatalf("scope %q [%d]: id = %q, want %q", tc.scope, i, st.ID, tc.want[i])
			}
		}
	}
}
