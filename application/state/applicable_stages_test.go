package state_test

import (
	"testing"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

func TestApplicableStages_PragmaticIsFlatRegardlessOfType(t *testing.T) {
	t.Parallel()
	want := []state.Stage{
		state.StageHydrate, state.StageImplement, state.StageCodeReview, state.StageCommit,
	}
	for _, st := range []state.StoryType{
		state.StoryTypeCode, state.StoryTypeDoc, state.StoryTypeMixed,
	} {
		got := appstate.ApplicableStages(st, appstate.ModePragmatic)
		if !stagesEq(got, want) {
			t.Errorf("pragmatic %s = %v, want %v", st, got, want)
		}
		if skipped := appstate.SkippedStages(st, appstate.ModePragmatic); len(skipped) != 0 {
			t.Errorf("pragmatic %s skipped = %v, want none", st, skipped)
		}
	}
}

func TestApplicableStages_StrictCodeIsFullPipeline(t *testing.T) {
	t.Parallel()
	got := appstate.ApplicableStages(state.StoryTypeCode, appstate.ModeStrict)
	want := []state.Stage{
		state.StageHydrate, state.StageATDD, state.StageImplement,
		state.StageAutomate, state.StageTestReview, state.StageCodeReview, state.StageCommit,
	}
	if !stagesEq(got, want) {
		t.Errorf("strict code = %v, want %v", got, want)
	}
}

func TestApplicableStages_StrictDocSkipsTestStages(t *testing.T) {
	t.Parallel()
	got := appstate.ApplicableStages(state.StoryTypeDoc, appstate.ModeStrict)
	want := []state.Stage{
		state.StageHydrate, state.StageImplement, state.StageCodeReview, state.StageCommit,
	}
	if !stagesEq(got, want) {
		t.Errorf("strict doc = %v, want %v", got, want)
	}
	skipped := appstate.SkippedStages(state.StoryTypeDoc, appstate.ModeStrict)
	wantSkipped := []state.Stage{state.StageATDD, state.StageAutomate, state.StageTestReview}
	if !stagesEq(skipped, wantSkipped) {
		t.Errorf("strict doc skipped = %v, want %v", skipped, wantSkipped)
	}
}

func TestApplicableStages_StrictMixedRunsFullPipeline(t *testing.T) {
	t.Parallel()
	got := appstate.ApplicableStages(state.StoryTypeMixed, appstate.ModeStrict)
	want := []state.Stage{
		state.StageHydrate, state.StageATDD, state.StageImplement,
		state.StageAutomate, state.StageTestReview, state.StageCodeReview, state.StageCommit,
	}
	if !stagesEq(got, want) {
		t.Errorf("strict mixed = %v, want %v", got, want)
	}
}

func stagesEq(a, b []state.Stage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
