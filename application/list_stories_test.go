package application_test

import (
	"testing"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type mockProgressStore struct {
	progress *domain.ProgressFile
}

func (m *mockProgressStore) Load(_ string) (*domain.ProgressFile, error) {
	return m.progress, nil
}

func (m *mockProgressStore) Save(_ string, _ *domain.ProgressFile) error {
	return nil
}

func intPtr(v int) *int { return &v }

func makeProgress() *domain.ProgressFile {
	return &domain.ProgressFile{
		Version:    1,
		DocsFolder: "/tmp/test",
		Stories: []*domain.Story{
			{ID: "1.1.auth-service", Status: domain.StatusComplete, ParallelGroup: intPtr(1)},
			{ID: "1.2.user-profile", Status: domain.StatusPending, ParallelGroup: intPtr(1), Blockers: []string{"1.1"}},
			{ID: "2.1.payment-api", Status: domain.StatusPending, ParallelGroup: intPtr(2)},
			{ID: "2.2.invoice-gen", Status: domain.StatusBlocked, ParallelGroup: intPtr(2), Blockers: []string{"2.1"}},
			{ID: "3.1.analytics", Status: domain.StatusInProgress, ParallelGroup: intPtr(3)},
		},
	}
}

func TestListStories_NoFilter(t *testing.T) {
	store := &mockProgressStore{progress: makeProgress()}
	uc := application.NewListStoriesUseCase(store, zap.NewNop())

	rows, err := uc.Execute("test.json", application.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Errorf("expected 5 rows, got %d", len(rows))
	}
}

func TestListStories_FilterByGroup(t *testing.T) {
	store := &mockProgressStore{progress: makeProgress()}
	uc := application.NewListStoriesUseCase(store, zap.NewNop())

	group := 2
	rows, err := uc.Execute("test.json", application.ListFilter{Group: &group})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows for group 2, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Story.ParallelGroup == nil || *r.Story.ParallelGroup != 2 {
			t.Errorf("story %s not in group 2", r.Story.ID)
		}
	}
}

func TestListStories_FilterByStatus(t *testing.T) {
	store := &mockProgressStore{progress: makeProgress()}
	uc := application.NewListStoriesUseCase(store, zap.NewNop())

	st := domain.StatusPending
	rows, err := uc.Execute("test.json", application.ListFilter{Status: &st})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 pending rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Story.Status != domain.StatusPending {
			t.Errorf("story %s has status %s, expected pending", r.Story.ID, r.Story.Status)
		}
	}
}

func TestListStories_FilterByGroupAndStatus(t *testing.T) {
	store := &mockProgressStore{progress: makeProgress()}
	uc := application.NewListStoriesUseCase(store, zap.NewNop())

	group := 1
	st := domain.StatusPending
	rows, err := uc.Execute("test.json", application.ListFilter{Group: &group, Status: &st})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row (group 1, pending), got %d", len(rows))
	}
	if len(rows) > 0 && rows[0].Story.ID != "1.2.user-profile" {
		t.Errorf("expected 1.2.user-profile, got %s", rows[0].Story.ID)
	}
}

func TestListStories_UnblockedOnly(t *testing.T) {
	store := &mockProgressStore{progress: makeProgress()}
	uc := application.NewListStoriesUseCase(store, zap.NewNop())

	rows, err := uc.Execute("test.json", application.ListFilter{UnblockedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	// 1.1 (complete, no blockers) ✓
	// 1.2 (pending, blocker 1.1 is complete) ✓
	// 2.1 (pending, no blockers) ✓
	// 2.2 (blocked, blocker 2.1 is NOT complete) ✗
	// 3.1 (in-progress, no blockers) ✓
	if len(rows) != 4 {
		t.Errorf("expected 4 unblocked rows, got %d", len(rows))
		for _, r := range rows {
			t.Logf("  %s (status=%s, blockers=%v)", r.Story.ID, r.Story.Status, r.Story.Blockers)
		}
	}
}

func TestListStories_ShowBlockersResolution(t *testing.T) {
	store := &mockProgressStore{progress: makeProgress()}
	uc := application.NewListStoriesUseCase(store, zap.NewNop())

	group := 1
	rows, err := uc.Execute("test.json", application.ListFilter{Group: &group})
	if err != nil {
		t.Fatal(err)
	}

	// Story 1.2 has blocker "1.1" which should be resolved (1.1 is complete)
	found := false
	for _, r := range rows {
		if r.Story.ID == "1.2.user-profile" {
			found = true
			if len(r.Blockers) != 1 {
				t.Errorf("expected 1 blocker, got %d", len(r.Blockers))
			}
			if len(r.Blockers) > 0 && !r.Blockers[0].Resolved {
				t.Error("blocker 1.1 should be resolved (story is complete)")
			}
		}
	}
	if !found {
		t.Error("story 1.2.user-profile not found in results")
	}
}
