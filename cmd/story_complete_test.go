package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// mockGitRunner captures the calls runCompleteOne makes and returns
// scripted outputs. It is intentionally not goroutine-safe beyond a
// simple mutex around the call log — the compound workflow runs
// serially within one command invocation.
type mockGitRunner struct {
	mu        sync.Mutex
	calls     []string
	statusOut string
	statusErr error
	addErr    error
	commitErr error
	commitSHA string
}

func (m *mockGitRunner) Status(ctx context.Context, repoDir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "status:"+repoDir)
	return m.statusOut, m.statusErr
}

func (m *mockGitRunner) Add(ctx context.Context, repoDir, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "add:"+repoDir+":"+path)
	return m.addErr
}

func (m *mockGitRunner) Commit(ctx context.Context, repoDir, message string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "commit:"+repoDir+":"+message)
	return m.commitSHA, m.commitErr
}

// setupCompleteTest creates a fresh sqlite DB, inserts one story whose
// File points at a tmp .md, and returns the wired service + paths the
// test will operate on.
func setupCompleteTest(t *testing.T) (*appstate.StoryService, func(), string, string) {
	t.Helper()

	// Override the package-level logger so PatchStatus doesn't NPE.
	log = zap.NewNop()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bmad-state.db")
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	storyFile := filepath.Join(dir, "story-1.1.md")
	if err := os.WriteFile(storyFile, []byte("# Story 1.1\n\n**Status:** in_progress\n"), 0o644); err != nil {
		t.Fatalf("write story file: %v", err)
	}

	svc := &appstate.StoryService{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Concerns:     sqlite.NewStoryConcernsStore(db),
		RetryCounts:  sqlite.NewStoryRetryCountsStore(db),
		Config:       sqlite.NewConfigStore(db),
		Checkpoints:  sqlite.NewCheckpointsStore(db),
	}

	st := state.Story{
		ID:         "1.1",
		File:       storyFile,
		Title:      "Test story",
		Status:     state.StatusInProgress,
		Complexity: state.ComplexityLow,
		StoryType:  state.StoryTypeCode,
	}
	if err := svc.Stories.Insert(context.Background(), st); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	cleanup := func() { _ = db.Close() }
	return svc, cleanup, storyFile, dir
}

func TestRunCompleteOne_AutoCommit_HappyPath(t *testing.T) {
	svc, cleanup, storyFile, dir := setupCompleteTest(t)
	defer cleanup()

	rel, err := filepath.Rel(dir, storyFile)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	rel = filepath.ToSlash(rel)

	mock := &mockGitRunner{
		statusOut: " M " + rel + "\n",
		commitSHA: "abc1234",
	}
	storyCompleteGitRunner = mock
	defer func() { storyCompleteGitRunner = infrastructure.ExecGitRunner{} }()

	if err := runCompleteOne(context.Background(), svc, "1.1", "", "", true /*autoCommit*/, false /*noCommit*/, dir); err != nil {
		t.Fatalf("runCompleteOne: %v", err)
	}

	// Status -> Add -> Commit, in that order.
	wantPrefixes := []string{"status:", "add:", "commit:"}
	if len(mock.calls) != 3 {
		t.Fatalf("calls=%v want 3 (status,add,commit)", mock.calls)
	}
	for i, p := range wantPrefixes {
		if !strings.HasPrefix(mock.calls[i], p) {
			t.Fatalf("call[%d]=%q want prefix %q", i, mock.calls[i], p)
		}
	}

	// The Add call should target the repo-relative story file path.
	if !strings.HasSuffix(mock.calls[1], ":"+rel) {
		t.Fatalf("add call %q must end with story path %q", mock.calls[1], rel)
	}

	// The commit message must reference the story id.
	if !strings.Contains(mock.calls[2], "mark 1.1 complete") {
		t.Fatalf("commit call %q missing story-id phrase", mock.calls[2])
	}

	// And the DB must reflect status=complete.
	got, err := svc.Stories.Get(context.Background(), "1.1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != state.StatusComplete {
		t.Fatalf("status=%s want complete", got.Status)
	}

	// And the story file's Status: line must now read "complete".
	body, err := os.ReadFile(storyFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "**Status:** complete") {
		t.Fatalf("story file Status line not patched:\n%s", string(body))
	}
}

func TestRunCompleteOne_AutoCommit_DirtyTree_Fails(t *testing.T) {
	svc, cleanup, storyFile, dir := setupCompleteTest(t)
	defer cleanup()

	rel, _ := filepath.Rel(dir, storyFile)
	rel = filepath.ToSlash(rel)

	mock := &mockGitRunner{
		statusOut: " M " + rel + "\n M cmd/root.go\n",
	}
	storyCompleteGitRunner = mock
	defer func() { storyCompleteGitRunner = infrastructure.ExecGitRunner{} }()

	err := runCompleteOne(context.Background(), svc, "1.1", "", "", true, false, dir)
	if err == nil {
		t.Fatalf("expected dirty-tree error, got nil")
	}
	if !strings.Contains(err.Error(), "working tree dirty") {
		t.Fatalf("error %q must mention dirty tree", err.Error())
	}
	if !strings.Contains(err.Error(), "cmd/root.go") {
		t.Fatalf("error %q should name the unrelated file", err.Error())
	}

	// Add + Commit must NOT have been called — only Status.
	if len(mock.calls) != 1 || !strings.HasPrefix(mock.calls[0], "status:") {
		t.Fatalf("dirty path should call Status only, got %v", mock.calls)
	}
}

func TestRunCompleteOne_AutoCommit_Idempotent(t *testing.T) {
	svc, cleanup, _, dir := setupCompleteTest(t)
	defer cleanup()

	// Pre-mark the story complete so the idempotency branch fires.
	if err := svc.Stories.SetComplete(context.Background(), "1.1", "", ""); err != nil {
		t.Fatalf("pre SetComplete: %v", err)
	}

	mock := &mockGitRunner{}
	storyCompleteGitRunner = mock
	defer func() { storyCompleteGitRunner = infrastructure.ExecGitRunner{} }()

	if err := runCompleteOne(context.Background(), svc, "1.1", "", "", true, false, dir); err != nil {
		t.Fatalf("runCompleteOne: %v", err)
	}

	// No git calls at all — idempotent no-op.
	if len(mock.calls) != 0 {
		t.Fatalf("idempotent re-run must not invoke git, got %v", mock.calls)
	}
}

func TestRunCompleteOne_NoCommitOverridesAutoCommit(t *testing.T) {
	svc, cleanup, _, dir := setupCompleteTest(t)
	defer cleanup()

	mock := &mockGitRunner{}
	storyCompleteGitRunner = mock
	defer func() { storyCompleteGitRunner = infrastructure.ExecGitRunner{} }()

	if err := runCompleteOne(context.Background(), svc, "1.1", "", "", true /*autoCommit*/, true /*noCommit*/, dir); err != nil {
		t.Fatalf("runCompleteOne: %v", err)
	}

	// Status should still be complete in the DB...
	got, _ := svc.Stories.Get(context.Background(), "1.1")
	if got.Status != state.StatusComplete {
		t.Fatalf("status=%s want complete", got.Status)
	}
	// ...but git was not invoked.
	if len(mock.calls) != 0 {
		t.Fatalf("no-commit override should suppress git, got %v", mock.calls)
	}
}
