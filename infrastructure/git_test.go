package infrastructure

import "testing"

func TestIsCleanForStoryFile(t *testing.T) {
	tests := []struct {
		name       string
		porcelain  string
		storyFile  string
		wantClean  bool
		wantReason string
	}{
		{
			name:      "empty porcelain - clean no-op",
			porcelain: "",
			storyFile: "docs/stories/1.1.md",
			wantClean: true,
		},
		{
			name:      "only story file modified - clean",
			porcelain: " M docs/stories/1.1.md",
			storyFile: "docs/stories/1.1.md",
			wantClean: true,
		},
		{
			name:       "unrelated file modified - dirty",
			porcelain:  " M docs/stories/1.1.md\n M cmd/root.go",
			storyFile:  "docs/stories/1.1.md",
			wantClean:  false,
			wantReason: "unrelated change: cmd/root.go",
		},
		{
			name:       "only unrelated file modified - dirty",
			porcelain:  "?? totally/random.txt",
			storyFile:  "docs/stories/1.1.md",
			wantClean:  false,
			wantReason: "unrelated change: totally/random.txt",
		},
		{
			name:       "rename detected - dirty (conservative)",
			porcelain:  "R  docs/stories/old.md -> docs/stories/new.md",
			storyFile:  "docs/stories/new.md",
			wantClean:  false,
			wantReason: "rename detected: docs/stories/old.md -> docs/stories/new.md",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotClean, gotReason := IsCleanForStoryFile(tc.porcelain, tc.storyFile)
			if gotClean != tc.wantClean {
				t.Fatalf("IsCleanForStoryFile clean=%v want %v (reason=%q)", gotClean, tc.wantClean, gotReason)
			}
			if tc.wantReason != "" && gotReason != tc.wantReason {
				t.Fatalf("IsCleanForStoryFile reason=%q want %q", gotReason, tc.wantReason)
			}
		})
	}
}
