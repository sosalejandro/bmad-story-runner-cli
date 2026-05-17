package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// StoriesStore is the sqlite adapter for state.Stories.
type StoriesStore struct{ db *DB }

func NewStoriesStore(db *DB) *StoriesStore { return &StoriesStore{db: db} }

const storiesSelectCols = `id, file, title, status, current_stage, parallel_group,
		hydrated_file, resource_budget_ram_mb, resource_budget_cpu_cores,
		requires_android, complexity, commit_hash, pr_url, ci_passed,
		completed_at, created_at, updated_at, claimed_at, claimed_by, story_type`

func scanStory(row interface {
	Scan(dest ...any) error
}) (state.Story, error) {
	var s state.Story
	var (
		currentStage sql.NullString
		parallelGrp  sql.NullInt64
		hydrated     sql.NullString
		ramMB        sql.NullInt64
		cpuCores     sql.NullFloat64
		requires     int
		commitHash   sql.NullString
		prURL        sql.NullString
		ciPassed     int
		completedAt  sql.NullTime
		claimedAt    sql.NullTime
		claimedBy    sql.NullString
	)
	if err := row.Scan(
		&s.ID, &s.File, &s.Title, &s.Status, &currentStage, &parallelGrp,
		&hydrated, &ramMB, &cpuCores, &requires, &s.Complexity, &commitHash,
		&prURL, &ciPassed, &completedAt, &s.CreatedAt, &s.UpdatedAt,
		&claimedAt, &claimedBy, &s.StoryType,
	); err != nil {
		return state.Story{}, err
	}
	s.ClaimedAt = ptrTime(claimedAt)
	s.ClaimedBy = ptrString(claimedBy)

	if currentStage.Valid {
		stage := state.Stage(currentStage.String)
		s.CurrentStage = &stage
	}
	s.ParallelGroup = ptrInt(parallelGrp)
	s.HydratedFile = ptrString(hydrated)
	if ramMB.Valid || cpuCores.Valid {
		rb := state.ResourceBudget{}
		if ramMB.Valid {
			rb.RamMB = int(ramMB.Int64)
		}
		if cpuCores.Valid {
			rb.CPUCores = cpuCores.Float64
		}
		s.ResourceBudget = &rb
	}
	s.RequiresAndroid = requires == 1
	s.CommitHash = ptrString(commitHash)
	s.PRURL = ptrString(prURL)
	s.CIPassed = ciPassed == 1
	s.CompletedAt = ptrTime(completedAt)
	return s, nil
}

func (s *StoriesStore) Get(ctx context.Context, id string) (state.Story, error) {
	row := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT `+storiesSelectCols+` FROM stories WHERE id = ?`, id)
	st, err := scanStory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Story{}, state.ErrNotFound
	}
	if err != nil {
		return state.Story{}, fmt.Errorf("stories get %q: %w", id, err)
	}
	return st, nil
}

func (s *StoriesStore) List(ctx context.Context, f state.StoryFilter) ([]state.Story, error) {
	var (
		where []string
		args  []any
	)
	if f.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*f.Status))
	}
	if f.CurrentStage != nil {
		where = append(where, "current_stage = ?")
		args = append(args, string(*f.CurrentStage))
	}
	if f.ParallelGroup != nil {
		where = append(where, "parallel_group = ?")
		args = append(args, *f.ParallelGroup)
	}
	if f.HasEnv != nil {
		if *f.HasEnv {
			where = append(where, "id IN (SELECT story_id FROM env_allocations WHERE reclaimed_at IS NULL)")
		} else {
			where = append(where, "id NOT IN (SELECT story_id FROM env_allocations WHERE reclaimed_at IS NULL)")
		}
	}
	if len(f.IDs) > 0 {
		placeholders := strings.Repeat("?,", len(f.IDs))
		placeholders = placeholders[:len(placeholders)-1]
		where = append(where, "id IN ("+placeholders+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}

	query := `SELECT ` + storiesSelectCols + ` FROM stories`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id"

	rows, err := s.db.sqlDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("stories list: %w", err)
	}
	defer rows.Close()

	var out []state.Story
	for rows.Next() {
		st, err := scanStory(rows)
		if err != nil {
			return nil, fmt.Errorf("stories scan: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *StoriesStore) Insert(ctx context.Context, st state.Story) error {
	var (
		stage    sql.NullString
		hydrated sql.NullString
		ramMB    sql.NullInt64
		cpu      sql.NullFloat64
	)
	if st.CurrentStage != nil {
		stage = sql.NullString{String: string(*st.CurrentStage), Valid: true}
	}
	if st.HydratedFile != nil {
		hydrated = sql.NullString{String: *st.HydratedFile, Valid: true}
	}
	if st.ResourceBudget != nil {
		ramMB = sql.NullInt64{Int64: int64(st.ResourceBudget.RamMB), Valid: true}
		cpu = sql.NullFloat64{Float64: st.ResourceBudget.CPUCores, Valid: true}
	}
	complexity := st.Complexity
	if complexity == "" {
		complexity = state.ComplexityMedium
	}
	storyType := st.StoryType
	if storyType == "" {
		storyType = state.StoryTypeCode
	}

	_, err := s.db.sqlDB().ExecContext(ctx, `
		INSERT INTO stories (
			id, file, title, status, current_stage, parallel_group,
			hydrated_file, resource_budget_ram_mb, resource_budget_cpu_cores,
			requires_android, complexity, commit_hash, pr_url, ci_passed,
			completed_at, story_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		st.ID, st.File, st.Title, string(st.Status),
		stage, nullInt(st.ParallelGroup),
		hydrated, ramMB, cpu,
		boolToInt(st.RequiresAndroid), string(complexity),
		nullString(st.CommitHash), nullString(st.PRURL),
		boolToInt(st.CIPassed), nullTime(st.CompletedAt),
		string(storyType),
	)
	if err != nil {
		return fmt.Errorf("stories insert %q: %w", st.ID, err)
	}
	return nil
}

func (s *StoriesStore) Update(ctx context.Context, st state.Story) error {
	var stage, hydrated sql.NullString
	if st.CurrentStage != nil {
		stage = sql.NullString{String: string(*st.CurrentStage), Valid: true}
	}
	if st.HydratedFile != nil {
		hydrated = sql.NullString{String: *st.HydratedFile, Valid: true}
	}
	var ramMB sql.NullInt64
	var cpu sql.NullFloat64
	if st.ResourceBudget != nil {
		ramMB = sql.NullInt64{Int64: int64(st.ResourceBudget.RamMB), Valid: true}
		cpu = sql.NullFloat64{Float64: st.ResourceBudget.CPUCores, Valid: true}
	}

	storyType := st.StoryType
	if storyType == "" {
		storyType = state.StoryTypeCode
	}
	res, err := s.db.sqlDB().ExecContext(ctx, `
		UPDATE stories SET
			file = ?, title = ?, status = ?, current_stage = ?, parallel_group = ?,
			hydrated_file = ?, resource_budget_ram_mb = ?, resource_budget_cpu_cores = ?,
			requires_android = ?, complexity = ?, commit_hash = ?, pr_url = ?,
			ci_passed = ?, completed_at = ?, story_type = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		st.File, st.Title, string(st.Status), stage, nullInt(st.ParallelGroup),
		hydrated, ramMB, cpu, boolToInt(st.RequiresAndroid), string(st.Complexity),
		nullString(st.CommitHash), nullString(st.PRURL),
		boolToInt(st.CIPassed), nullTime(st.CompletedAt), string(storyType), st.ID,
	)
	if err != nil {
		return fmt.Errorf("stories update %q: %w", st.ID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *StoriesStore) SetStatus(ctx context.Context, id string, status state.Status) error {
	res, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE stories SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(status), id)
	if err != nil {
		return fmt.Errorf("stories set-status %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *StoriesStore) SetCurrentStage(ctx context.Context, id string, stage *state.Stage) error {
	var ns sql.NullString
	if stage != nil {
		ns = sql.NullString{String: string(*stage), Valid: true}
	}
	res, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE stories SET current_stage = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		ns, id)
	if err != nil {
		return fmt.Errorf("stories set-stage %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *StoriesStore) SetComplete(ctx context.Context, id, commitHash, prURL string) error {
	res, err := s.db.sqlDB().ExecContext(ctx, `
		UPDATE stories SET
			status = ?, current_stage = ?, ci_passed = 1,
			commit_hash = ?, pr_url = ?,
			completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		string(state.StatusComplete), string(state.StageDone), commitHash, prURL, id)
	if err != nil {
		return fmt.Errorf("stories set-complete %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ClaimUnclaimedPending picks up to `max` candidates whose status=pending AND
// claimed_at IS NULL AND id IN (eligibleIDs), atomically marks them claimed,
// and returns the freshly-claimed rows. Single write transaction ensures two
// concurrent callers can never both claim the same story.
func (s *StoriesStore) ClaimUnclaimedPending(
	ctx context.Context, eligibleIDs []string, max int, claimedBy string,
) ([]state.Story, error) {
	if max <= 0 || len(eligibleIDs) == 0 {
		return nil, nil
	}

	tx, err := s.db.sqlDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("stories claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Pick candidates from the eligible set: unclaimed + pending.
	placeholders := strings.Repeat("?,", len(eligibleIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(eligibleIDs)+2)
	for _, id := range eligibleIDs {
		args = append(args, id)
	}
	args = append(args, string(state.StatusPending), max)
	query := `SELECT id FROM stories
	          WHERE id IN (` + placeholders + `)
	            AND status = ?
	            AND claimed_at IS NULL
	          ORDER BY id LIMIT ?`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("stories claim pick: %w", err)
	}
	var pickedIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("stories claim scan: %w", err)
		}
		pickedIDs = append(pickedIDs, id)
	}
	_ = rows.Close()
	if len(pickedIDs) == 0 {
		_ = tx.Commit()
		return nil, nil
	}

	// Mark the picked rows claimed in the same transaction.
	updateArgs := make([]any, 0, len(pickedIDs)+1)
	updateArgs = append(updateArgs, claimedBy)
	for _, id := range pickedIDs {
		updateArgs = append(updateArgs, id)
	}
	updatePlaceholders := strings.Repeat("?,", len(pickedIDs))
	updatePlaceholders = updatePlaceholders[:len(updatePlaceholders)-1]
	if _, err := tx.ExecContext(ctx, `
		UPDATE stories
		   SET claimed_at = CURRENT_TIMESTAMP, claimed_by = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id IN (`+updatePlaceholders+`)
		   AND claimed_at IS NULL
	`, updateArgs...); err != nil {
		return nil, fmt.Errorf("stories claim update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("stories claim commit: %w", err)
	}

	// Hydrate the rows in a fresh read (post-commit) so callers see the
	// claimed_at timestamps.
	out := make([]state.Story, 0, len(pickedIDs))
	for _, id := range pickedIDs {
		st, err := s.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("stories claim re-read %q: %w", id, err)
		}
		out = append(out, st)
	}
	return out, nil
}

// ReleaseClaim clears claimed_at + claimed_by. Idempotent — clearing an
// already-clear row is not an error.
func (s *StoriesStore) ReleaseClaim(ctx context.Context, id string) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE stories SET claimed_at = NULL, claimed_by = NULL,
		                    updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("stories release-claim %q: %w", id, err)
	}
	return nil
}
