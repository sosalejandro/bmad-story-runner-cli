package infrastructure

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

var (
	headingRe      = regexp.MustCompile(`^#\s+(.+)$`)
	statusRe       = regexp.MustCompile(`(?i)\*\*Status:\*\*\s*(.+)`)
	blockerIDRe    = regexp.MustCompile(`(\d+\.\d+[\w.-]*)`)
	storyIDRe      = regexp.MustCompile(`^(\d+\.\d+[\w.-]*)`)
	taskDoneRe     = regexp.MustCompile(`\[x\]`)
	taskTotalRe    = regexp.MustCompile(`\[(x| )\]`)
	acRe           = regexp.MustCompile(`^[0-9]+\.`)
	placeholderStr = "To be populated"
)

// FSStoryScanner discovers story .md files and parses their metadata.
type FSStoryScanner struct {
	log *zap.Logger
}

func NewFSStoryScanner(log *zap.Logger) *FSStoryScanner {
	return &FSStoryScanner{log: log}
}

func (sc *FSStoryScanner) Scan(docsFolder string) ([]*domain.Story, error) {
	var files []string
	err := filepath.WalkDir(docsFolder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %q: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.EqualFold(name, "readme.md") || name == "bmad-progress.json" {
			return nil
		}
		if strings.ToLower(filepath.Ext(name)) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning docs folder %q: %w", docsFolder, err)
	}

	sort.Slice(files, func(i, j int) bool {
		return storyFileSortKey(files[i]) < storyFileSortKey(files[j])
	})

	stories := make([]*domain.Story, 0, len(files))
	var parseErrs []error
	for _, f := range files {
		story, err := parseStoryFile(f, docsFolder)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("parsing %q: %w", f, err))
			continue
		}
		sc.log.Debug("discovered story", zap.String("id", story.ID), zap.String("file", story.File))
		stories = append(stories, story)
	}

	if err := domain.Join(parseErrs...); err != nil {
		return nil, fmt.Errorf("parsing story files: %w", err)
	}

	return stories, nil
}

func parseStoryFile(path, docsRoot string) (*domain.Story, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	text := string(content)
	lines := strings.Split(text, "\n")

	title := filepath.Base(path)
	if m := headingRe.FindStringSubmatch(text); m != nil {
		title = strings.TrimSpace(m[1])
	}

	var status domain.Status = domain.StatusPending
	if m := statusRe.FindStringSubmatch(text); m != nil {
		raw := strings.ToLower(strings.TrimRight(strings.TrimSpace(m[1]), "*"))
		if mapped, ok := domain.StatusFromFileHint[raw]; ok {
			status = mapped
		}
	}

	var blockers []string
	inBlockerSection := false
	for _, line := range lines {
		if regexp.MustCompile(`(?i)^##\s+(Blockers|Dependencies)`).MatchString(line) {
			inBlockerSection = true
			continue
		}
		if inBlockerSection {
			if strings.HasPrefix(line, "##") {
				break
			}
			if m := blockerIDRe.FindString(line); m != "" {
				blockers = append(blockers, m)
			}
		}
	}

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	storyID := stem
	if m := storyIDRe.FindString(stem); m != "" {
		storyID = m
	}

	relPath, err := filepath.Rel(docsRoot, path)
	if err != nil {
		return nil, fmt.Errorf("computing relative path: %w", err)
	}

	if blockers == nil {
		blockers = []string{}
	}

	return &domain.Story{
		ID:         storyID,
		File:       relPath,
		Title:      title,
		Status:     status,
		Blockers:   blockers,
		QAConcerns: []domain.QAConcern{},
	}, nil
}

func storyFileSortKey(path string) string {
	return filepath.Base(path)
}

// FSStoryScanReporter reports task completion counts for story files.
type FSStoryScanReporter struct {
	log *zap.Logger
}

func NewFSStoryScanReporter(log *zap.Logger) *FSStoryScanReporter {
	return &FSStoryScanReporter{log: log}
}

func (r *FSStoryScanReporter) Report(docsFolder string) ([]*ports.StoryScanResult, error) {
	scanner := NewFSStoryScanner(r.log)
	stories, err := scanner.Scan(docsFolder)
	if err != nil {
		return nil, err
	}

	results := make([]*ports.StoryScanResult, 0, len(stories))
	var scanErrs []error

	for _, s := range stories {
		fullPath := filepath.Join(docsFolder, s.File)
		counts, err := countTasksInFile(fullPath)
		if err != nil {
			scanErrs = append(scanErrs, fmt.Errorf("counting tasks in %q: %w", fullPath, err))
			continue
		}
		results = append(results, &ports.StoryScanResult{
			StoryID:    s.ID,
			File:       s.File,
			Title:      s.Title,
			ACCount:    counts.acCount,
			TasksDone:  counts.tasksDone,
			TasksTotal: counts.tasksTotal,
		})
	}

	if err := domain.Join(scanErrs...); err != nil {
		return nil, fmt.Errorf("scan report errors: %w", err)
	}

	return results, nil
}

type taskCounts struct {
	acCount    int
	tasksDone  int
	tasksTotal int
}

func countTasksInFile(path string) (*taskCounts, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	counts := &taskCounts{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if acRe.MatchString(strings.TrimSpace(line)) {
			counts.acCount++
		}
		if taskTotalRe.MatchString(line) {
			counts.tasksTotal++
			if taskDoneRe.MatchString(line) {
				counts.tasksDone++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning file: %w", err)
	}

	return counts, nil
}

// FSQAPendingScanner finds story files containing the QA placeholder string.
type FSQAPendingScanner struct {
	log *zap.Logger
}

func NewFSQAPendingScanner(log *zap.Logger) *FSQAPendingScanner {
	return &FSQAPendingScanner{log: log}
}

func (sc *FSQAPendingScanner) FindPending(docsFolder string) ([]string, error) {
	var pending []string
	err := filepath.WalkDir(docsFolder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %q: %w", path, err)
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(d.Name())) != ".md" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %q: %w", path, err)
		}

		if strings.Contains(string(content), placeholderStr) {
			pending = append(pending, path)
			sc.log.Debug("qa placeholder found", zap.String("file", path))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning for qa-pending in %q: %w", docsFolder, err)
	}

	return pending, nil
}
