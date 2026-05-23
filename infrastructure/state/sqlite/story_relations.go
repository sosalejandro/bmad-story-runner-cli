package sqlite

import (
	"context"
	"fmt"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// StoryDependenciesStore — sqlite adapter for state.StoryDependencies.
type StoryDependenciesStore struct{ db *DB }

func NewStoryDependenciesStore(db *DB) *StoryDependenciesStore {
	return &StoryDependenciesStore{db: db}
}

func (s *StoryDependenciesStore) Add(ctx context.Context, storyID, dependsOnID string) error {
	// Pre-#54 entry point — defaults to the 'explicit' kind so callers that
	// don't carry synthesis context (manual repair tooling, raw CRUD)
	// preserve the historical semantics.
	return s.AddWithKind(ctx, storyID, dependsOnID, state.EdgeKindExplicit)
}

// AddWithKind inserts a tagged edge. INSERT OR IGNORE keeps the call
// idempotent against the (story_id, depends_on_id) primary key — when a
// row already exists we deliberately do NOT overwrite its edge_kind. This
// means a row previously written as 'explicit' (e.g. by a story author's
// depends_on) wins over a later 'epic_synth' that points at the same
// upstream story; the planner's wipe-then-replace flow (RemoveAllFor +
// AddWithKind) guarantees the right kind on every re-ingest, so this
// preserve-on-conflict policy is only the safety net.
func (s *StoryDependenciesStore) AddWithKind(ctx context.Context, storyID, dependsOnID string, kind state.DependencyEdgeKind) error {
	if kind == "" {
		kind = state.EdgeKindExplicit
	}
	_, err := s.db.sqlDB().ExecContext(ctx,
		`INSERT OR IGNORE INTO story_dependencies (story_id, depends_on_id, edge_kind) VALUES (?, ?, ?)`,
		storyID, dependsOnID, string(kind))
	if err != nil {
		return fmt.Errorf("story_dependencies add %q->%q (%s): %w", storyID, dependsOnID, kind, err)
	}
	return nil
}

func (s *StoryDependenciesStore) Remove(ctx context.Context, storyID, dependsOnID string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`DELETE FROM story_dependencies WHERE story_id = ? AND depends_on_id = ?`,
		storyID, dependsOnID)
	if err != nil {
		return fmt.Errorf("story_dependencies remove: %w", err)
	}
	return nil
}

// RemoveAllFor wipes every dependency row for storyID. See port docstring —
// used by the planner for idempotent re-ingest.
func (s *StoryDependenciesStore) RemoveAllFor(ctx context.Context, storyID string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`DELETE FROM story_dependencies WHERE story_id = ?`,
		storyID)
	if err != nil {
		return fmt.Errorf("story_dependencies remove-all-for %q: %w", storyID, err)
	}
	return nil
}

func (s *StoryDependenciesStore) Of(ctx context.Context, storyID string) ([]string, error) {
	return queryIDs(ctx, s.db,
		`SELECT depends_on_id FROM story_dependencies WHERE story_id = ? ORDER BY depends_on_id`,
		storyID)
}

// EdgesOf returns each dependency row for storyID with its edge_kind, sorted
// deterministically by depends_on_id. Consumers that need the kind (graph
// renderer, validate-deps placeholder suppression) call this; consumers
// that only care about the prereq id list keep using `Of` to avoid a kind
// allocation per row.
func (s *StoryDependenciesStore) EdgesOf(ctx context.Context, storyID string) ([]state.DependencyEdge, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT depends_on_id, edge_kind FROM story_dependencies
		 WHERE story_id = ? ORDER BY depends_on_id`,
		storyID)
	if err != nil {
		return nil, fmt.Errorf("story_dependencies edges-of %q: %w", storyID, err)
	}
	defer rows.Close()
	var out []state.DependencyEdge
	for rows.Next() {
		e := state.DependencyEdge{StoryID: storyID}
		var kind string
		if err := rows.Scan(&e.DependsOnID, &kind); err != nil {
			return nil, fmt.Errorf("story_dependencies edges-of %q scan: %w", storyID, err)
		}
		e.Kind = state.DependencyEdgeKind(kind)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *StoryDependenciesStore) DependentsOf(ctx context.Context, storyID string) ([]string, error) {
	return queryIDs(ctx, s.db,
		`SELECT story_id FROM story_dependencies WHERE depends_on_id = ? ORDER BY story_id`,
		storyID)
}

// QuerySyntheticEdges returns every story_dependencies row whose edge_kind
// is not 'explicit' — i.e. the rows the planner wrote from epic-level
// synthesis (issue #46) and tagged via #54. The caller (validate-deps cobra
// shim) reshapes the slice into a SyntheticEdgeSet for the validator.
//
// Lives as a package-level function (not a method on StoryDependenciesStore)
// because the consumer doesn't otherwise need the store — keeping the call
// site free of an unused adapter handle. Returns an empty slice (no error)
// when the table is empty; a non-existent column would surface at migration
// time, never here.
func QuerySyntheticEdges(ctx context.Context, db *DB) ([]state.DependencyEdge, error) {
	rows, err := db.sqlDB().QueryContext(ctx,
		`SELECT story_id, depends_on_id, edge_kind FROM story_dependencies
		 WHERE edge_kind != ? ORDER BY story_id, depends_on_id`,
		string(state.EdgeKindExplicit))
	if err != nil {
		return nil, fmt.Errorf("query synthetic edges: %w", err)
	}
	defer rows.Close()
	var out []state.DependencyEdge
	for rows.Next() {
		var e state.DependencyEdge
		var kind string
		if err := rows.Scan(&e.StoryID, &e.DependsOnID, &kind); err != nil {
			return nil, fmt.Errorf("query synthetic edges scan: %w", err)
		}
		e.Kind = state.DependencyEdgeKind(kind)
		out = append(out, e)
	}
	return out, rows.Err()
}

// StoryAffectsStore — sqlite adapter for state.StoryAffects.
type StoryAffectsStore struct{ db *DB }

func NewStoryAffectsStore(db *DB) *StoryAffectsStore { return &StoryAffectsStore{db: db} }

func (s *StoryAffectsStore) Add(ctx context.Context, storyID, path string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`INSERT OR IGNORE INTO story_affects (story_id, path) VALUES (?, ?)`,
		storyID, path)
	if err != nil {
		return fmt.Errorf("story_affects add: %w", err)
	}
	return nil
}

func (s *StoryAffectsStore) Remove(ctx context.Context, storyID, path string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`DELETE FROM story_affects WHERE story_id = ? AND path = ?`,
		storyID, path)
	if err != nil {
		return fmt.Errorf("story_affects remove: %w", err)
	}
	return nil
}

// RemoveAllFor wipes every affects row for storyID. See port docstring —
// used by the planner for idempotent re-ingest.
func (s *StoryAffectsStore) RemoveAllFor(ctx context.Context, storyID string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`DELETE FROM story_affects WHERE story_id = ?`,
		storyID)
	if err != nil {
		return fmt.Errorf("story_affects remove-all-for %q: %w", storyID, err)
	}
	return nil
}

func (s *StoryAffectsStore) Of(ctx context.Context, storyID string) ([]string, error) {
	return queryIDs(ctx, s.db,
		`SELECT path FROM story_affects WHERE story_id = ? ORDER BY path`,
		storyID)
}

func (s *StoryAffectsStore) StoriesAffecting(ctx context.Context, path string) ([]string, error) {
	return queryIDs(ctx, s.db,
		`SELECT story_id FROM story_affects WHERE path = ? ORDER BY story_id`,
		path)
}

// StoryConcernsStore — sqlite adapter for state.StoryConcerns.
type StoryConcernsStore struct{ db *DB }

func NewStoryConcernsStore(db *DB) *StoryConcernsStore { return &StoryConcernsStore{db: db} }

func (s *StoryConcernsStore) Add(ctx context.Context, storyID, source, bodyJSON string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`INSERT INTO story_concerns (story_id, source, body_json) VALUES (?, ?, ?)`,
		storyID, source, bodyJSON)
	if err != nil {
		return fmt.Errorf("story_concerns add: %w", err)
	}
	return nil
}

func (s *StoryConcernsStore) Of(ctx context.Context, storyID string) ([]state.Concern, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT id, story_id, appended_at, source, body_json FROM story_concerns
		 WHERE story_id = ? ORDER BY id`, storyID)
	if err != nil {
		return nil, fmt.Errorf("story_concerns list: %w", err)
	}
	defer rows.Close()

	var out []state.Concern
	for rows.Next() {
		var c state.Concern
		if err := rows.Scan(&c.ID, &c.StoryID, &c.AppendedAt, &c.Source, &c.BodyJSON); err != nil {
			return nil, fmt.Errorf("story_concerns scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// StoryRetryCountsStore — sqlite adapter for state.StoryRetryCounts.
type StoryRetryCountsStore struct{ db *DB }

func NewStoryRetryCountsStore(db *DB) *StoryRetryCountsStore {
	return &StoryRetryCountsStore{db: db}
}

func (s *StoryRetryCountsStore) Get(ctx context.Context, storyID string) (state.RetryCounts, error) {
	var rc state.RetryCounts
	rc.StoryID = storyID
	err := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT tdd_cycles, qa_cycles, ci_retries, review_iterations
		 FROM story_retry_counts WHERE story_id = ?`, storyID).
		Scan(&rc.TDDCycles, &rc.QACycles, &rc.CIRetries, &rc.ReviewIterations)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			// row absence is "all zero" — no error.
			return rc, nil
		}
		return state.RetryCounts{}, fmt.Errorf("story_retry_counts get %q: %w", storyID, err)
	}
	return rc, nil
}

func (s *StoryRetryCountsStore) IncTDD(ctx context.Context, storyID string) error {
	return s.inc(ctx, storyID, "tdd_cycles")
}

func (s *StoryRetryCountsStore) IncQA(ctx context.Context, storyID string) error {
	return s.inc(ctx, storyID, "qa_cycles")
}

func (s *StoryRetryCountsStore) IncCI(ctx context.Context, storyID string) error {
	return s.inc(ctx, storyID, "ci_retries")
}

func (s *StoryRetryCountsStore) IncReview(ctx context.Context, storyID string) error {
	return s.inc(ctx, storyID, "review_iterations")
}

func (s *StoryRetryCountsStore) Reset(ctx context.Context, storyID string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE story_retry_counts
		 SET tdd_cycles=0, qa_cycles=0, ci_retries=0, review_iterations=0
		 WHERE story_id = ?`, storyID)
	if err != nil {
		return fmt.Errorf("story_retry_counts reset %q: %w", storyID, err)
	}
	return nil
}

func (s *StoryRetryCountsStore) inc(ctx context.Context, storyID, column string) error {
	// Upsert: create row at 1 if absent, else +1. Column name is a hardcoded
	// whitelist via the public Inc* methods, never user input.
	q := fmt.Sprintf(`
		INSERT INTO story_retry_counts (story_id, %s) VALUES (?, 1)
		ON CONFLICT(story_id) DO UPDATE SET %s = %s + 1
	`, column, column, column)
	_, err := s.db.sqlDB().ExecContext(ctx, q, storyID)
	if err != nil {
		return fmt.Errorf("story_retry_counts inc %s for %q: %w", column, storyID, err)
	}
	return nil
}

// queryIDs is a tiny helper for the "SELECT one TEXT column" pattern shared
// across the relation stores.
func queryIDs(ctx context.Context, db *DB, query string, args ...any) ([]string, error) {
	rows, err := db.sqlDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
