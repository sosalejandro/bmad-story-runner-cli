package application

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// AssignGroupsUseCase distributes stories across N parallel groups by epic/module.
// Stories that share the same top-level directory (or the same first numeric ID segment
// when the layout is flat) are always kept in the same group to minimise merge conflicts.
// Groups are balanced by story count using a greedy min-heap approach.
type AssignGroupsUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewAssignGroupsUseCase(store ports.ProgressStore, log *zap.Logger) *AssignGroupsUseCase {
	return &AssignGroupsUseCase{store: store, log: log}
}

// GroupAssignment describes what was assigned to each group.
type GroupAssignment struct {
	Group   int
	Modules []string
	Count   int
}

// AssignGroupsResult is the summary returned to the caller.
type AssignGroupsResult struct {
	Total       int
	Groups      []GroupAssignment
	AlreadySet  int // stories that already had a group (skipped)
}

// Execute loads the progress file, computes module groupings, assigns parallel_group to
// every story that does not already have one, and saves the file.
// If force is true, existing group assignments are overwritten.
func (uc *AssignGroupsUseCase) Execute(progressPath string, nGroups int, force bool) (*AssignGroupsResult, error) {
	if nGroups < 1 {
		return nil, fmt.Errorf("number of groups must be ≥ 1, got %d", nGroups)
	}

	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return nil, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	// Count stories that already have a group set.
	alreadySet := 0
	for _, s := range progress.Stories {
		if s.ParallelGroup != nil {
			alreadySet++
		}
	}
	if alreadySet > 0 && !force {
		return nil, fmt.Errorf(
			"%d stories already have group assignments; use --force to overwrite",
			alreadySet,
		)
	}

	// ── Step 1: cluster stories by module key ─────────────────────────────────
	// Module key is the top-level directory of the story file.
	// If the file has no directory component (flat layout), fall back to the
	// first numeric segment of the story ID (e.g. "1" from "1.2.some-story").
	type moduleGroup struct {
		key     string
		sortKey []int
		stories []*domain.Story
	}

	moduleMap := map[string]*moduleGroup{}
	for _, s := range progress.Stories {
		key := moduleKey(s)
		mg, ok := moduleMap[key]
		if !ok {
			mg = &moduleGroup{key: key, sortKey: moduleKeySort(key)}
			moduleMap[key] = mg
		}
		mg.stories = append(mg.stories, s)
	}

	// Sort modules by natural order.
	modules := make([]*moduleGroup, 0, len(moduleMap))
	for _, mg := range moduleMap {
		modules = append(modules, mg)
	}
	sort.Slice(modules, func(i, j int) bool {
		return compareIntSlice(modules[i].sortKey, modules[j].sortKey) < 0
	})

	// ── Step 2: greedy bin-packing — assign each module to the least-loaded group ──
	groupCounts := make([]int, nGroups)       // story count per group (0-indexed)
	groupModules := make([][]string, nGroups) // module keys per group (0-indexed)

	for _, mg := range modules {
		// Find the group with the fewest stories; break ties by lower group index.
		minIdx := 0
		for g := 1; g < nGroups; g++ {
			if groupCounts[g] < groupCounts[minIdx] {
				minIdx = g
			}
		}
		groupCounts[minIdx] += len(mg.stories)
		groupModules[minIdx] = append(groupModules[minIdx], mg.key)
		g := minIdx + 1 // groups are 1-indexed
		for _, s := range mg.stories {
			s.ParallelGroup = intPtr(g)
		}
		uc.log.Debug("assigned module to group",
			zap.String("module", mg.key),
			zap.Int("group", g),
			zap.Int("stories", len(mg.stories)),
		)
	}

	if err := uc.store.Save(progressPath, progress); err != nil {
		return nil, fmt.Errorf("saving progress file %q: %w", progressPath, err)
	}

	assignments := make([]GroupAssignment, nGroups)
	for i := range assignments {
		assignments[i] = GroupAssignment{
			Group:   i + 1,
			Modules: groupModules[i],
			Count:   groupCounts[i],
		}
	}

	return &AssignGroupsResult{
		Total:      len(progress.Stories),
		Groups:     assignments,
		AlreadySet: alreadySet,
	}, nil
}

// moduleKey returns the clustering key for a story.
// It is the top-level subdirectory of the story file path within the docs folder,
// or (for flat layouts) the first numeric segment of the story ID.
func moduleKey(s *domain.Story) string {
	dir := filepath.Dir(s.File)
	if dir != "" && dir != "." {
		// Use only the top-level directory component.
		parts := strings.SplitN(filepath.ToSlash(dir), "/", 2)
		return parts[0]
	}
	// Flat layout: derive key from first numeric segment of ID.
	idParts := strings.SplitN(s.ID, ".", 2)
	return idParts[0]
}

// moduleKeySort converts a module key to a comparable []int for natural ordering.
func moduleKeySort(key string) []int {
	var nums []int
	for _, p := range strings.Split(key, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			// Non-numeric: use string byte values as a fallback.
			for _, b := range []byte(p) {
				nums = append(nums, int(b))
			}
			continue
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return []int{999}
	}
	return nums
}

func compareIntSlice(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return len(a) - len(b)
}

func intPtr(n int) *int { return &n }
