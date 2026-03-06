package infrastructure

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

const gatesSubdir = "qa/gates"

type gateFileYAML struct {
	Gate     string `yaml:"gate"`
	Concerns []struct {
		Severity string `yaml:"severity"`
		Note     string `yaml:"note"`
	} `yaml:"concerns"`
}

// FSGateReader reads QA gate YAML files from <docs_folder>/qa/gates/*.yml.
type FSGateReader struct {
	log *zap.Logger
}

func NewFSGateReader(log *zap.Logger) *FSGateReader {
	return &FSGateReader{log: log}
}

func (r *FSGateReader) ReadGates(docsFolder string) ([]*domain.StoryGate, error) {
	gatesDir := filepath.Join(docsFolder, gatesSubdir)

	if _, err := os.Stat(gatesDir); os.IsNotExist(err) {
		r.log.Warn("gates directory does not exist", zap.String("path", gatesDir))
		return nil, nil
	}

	var gates []*domain.StoryGate
	var readErrs []error

	err := filepath.WalkDir(gatesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %q: %w", path, err)
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(d.Name())) != ".yml" {
			return nil
		}

		gate, err := r.readGateFile(path)
		if err != nil {
			readErrs = append(readErrs, fmt.Errorf("gate file %q: %w", path, err))
			return nil
		}

		gates = append(gates, gate)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking gates directory %q: %w", gatesDir, err)
	}

	if err := domain.Join(readErrs...); err != nil {
		return nil, fmt.Errorf("reading gate files: %w", err)
	}

	return gates, nil
}

func (r *FSGateReader) readGateFile(path string) (*domain.StoryGate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	var raw gateFileYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	result, err := domain.ParseGateResult(strings.ToUpper(strings.TrimSpace(raw.Gate)))
	if err != nil {
		return nil, fmt.Errorf("invalid gate value: %w", err)
	}

	// Story ID derived from filename: "3.2.story-name.yml" -> "3.2"
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	storyID := stem
	if m := storyIDRe.FindString(stem); m != "" {
		storyID = m
	}

	var concerns []domain.QAConcern
	for _, c := range raw.Concerns {
		concerns = append(concerns, domain.QAConcern{
			Severity: c.Severity,
			Note:     c.Note,
		})
	}

	r.log.Debug("gate file read", zap.String("story_id", storyID), zap.String("result", string(result)))

	return &domain.StoryGate{
		StoryID:  storyID,
		Result:   result,
		Concerns: concerns,
	}, nil
}
