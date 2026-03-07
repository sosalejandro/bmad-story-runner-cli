package infrastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// progressFileJSON is the on-disk representation, with time fields as strings for portability.
// Version is json.Number to tolerate both int (1) and string ("1") in existing files.
type progressFileJSON struct {
	Version     json.Number  `json:"version"`
	DocsFolder  string       `json:"docs_folder"`
	LastUpdated string       `json:"last_updated"`
	Stories     []*storyJSON `json:"stories"`
}

type storyJSON struct {
	ID              string             `json:"id"`
	File            string             `json:"file"`
	Title           string             `json:"title"`
	Status          string             `json:"status"`
	ParallelGroup   *int               `json:"parallel_group"`
	AssignedSession *string            `json:"assigned_session"`
	Blockers        []string           `json:"blockers"`
	QAConcerns      []domain.QAConcern `json:"qa_concerns"`
	CIPassed        bool               `json:"ci_passed"`
	CompletedAt     *string            `json:"completed_at"`
}

const timeLayout = "2006-01-02T15:04:05Z"

type JSONProgressStore struct {
	log *zap.Logger
}

func NewJSONProgressStore(log *zap.Logger) *JSONProgressStore {
	return &JSONProgressStore{log: log}
}

func (s *JSONProgressStore) Load(path string) (*domain.ProgressFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("progress file %q does not exist: %w", path, err)
		}
		return nil, fmt.Errorf("reading progress file %q: %w", path, err)
	}

	var raw progressFileJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshalling progress file %q: %w", path, err)
	}

	lastUpdated, err := time.Parse(timeLayout, raw.LastUpdated)
	if err != nil {
		return nil, fmt.Errorf("parsing last_updated in %q: %w", path, err)
	}

	stories := make([]*domain.Story, 0, len(raw.Stories))
	var parseErrs []error
	for _, sj := range raw.Stories {
		story, err := storyFromJSON(sj)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("story %q: %w", sj.ID, err))
			continue
		}
		stories = append(stories, story)
	}

	if err := domain.Join(parseErrs...); err != nil {
		return nil, fmt.Errorf("parsing stories in %q: %w", path, err)
	}

	version := 1 // default
	if v, err := raw.Version.Int64(); err == nil {
		version = int(v)
	} else if f, err := raw.Version.Float64(); err == nil {
		version = int(f)
	}

	return &domain.ProgressFile{
		Version:     version,
		DocsFolder:  raw.DocsFolder,
		LastUpdated: lastUpdated,
		Stories:     stories,
	}, nil
}

func (s *JSONProgressStore) Save(path string, progress *domain.ProgressFile) error {
	progress.LastUpdated = time.Now().UTC()

	raw := progressFileJSON{
		Version:     json.Number(strconv.Itoa(progress.Version)),
		DocsFolder:  progress.DocsFolder,
		LastUpdated: progress.LastUpdated.Format(timeLayout),
		Stories:     make([]*storyJSON, len(progress.Stories)),
	}

	for i, story := range progress.Stories {
		raw.Stories[i] = storyToJSON(story)
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling progress file: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing progress file %q: %w", path, err)
	}

	s.log.Debug("progress file saved", zap.String("path", path))
	return nil
}

func storyFromJSON(sj *storyJSON) (*domain.Story, error) {
	status, err := domain.ParseStatus(sj.Status)
	if err != nil {
		return nil, fmt.Errorf("parsing status: %w", err)
	}

	var completedAt *time.Time
	if sj.CompletedAt != nil {
		t, err := time.Parse(timeLayout, *sj.CompletedAt)
		if err != nil {
			return nil, fmt.Errorf("parsing completed_at: %w", err)
		}
		completedAt = &t
	}

	blockers := sj.Blockers
	if blockers == nil {
		blockers = []string{}
	}
	concerns := sj.QAConcerns
	if concerns == nil {
		concerns = []domain.QAConcern{}
	}

	return &domain.Story{
		ID:              sj.ID,
		File:            sj.File,
		Title:           sj.Title,
		Status:          status,
		ParallelGroup:   sj.ParallelGroup,
		AssignedSession: sj.AssignedSession,
		Blockers:        blockers,
		QAConcerns:      concerns,
		CIPassed:        sj.CIPassed,
		CompletedAt:     completedAt,
	}, nil
}

func storyToJSON(s *domain.Story) *storyJSON {
	sj := &storyJSON{
		ID:              s.ID,
		File:            s.File,
		Title:           s.Title,
		Status:          string(s.Status),
		ParallelGroup:   s.ParallelGroup,
		AssignedSession: s.AssignedSession,
		Blockers:        s.Blockers,
		QAConcerns:      s.QAConcerns,
		CIPassed:        s.CIPassed,
	}
	if s.CompletedAt != nil {
		t := s.CompletedAt.Format(timeLayout)
		sj.CompletedAt = &t
	}
	return sj
}
