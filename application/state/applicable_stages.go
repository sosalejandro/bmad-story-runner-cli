package state

import (
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// Mode mirrors the runtime config key "mode".
type Mode string

const (
	ModePragmatic Mode = "pragmatic"
	ModeStrict    Mode = "strict"
)

// ApplicableStages returns the stages the orchestrator must dispatch
// for a story, in execution order. Stages not in this list should be
// pre-recorded by the orchestrator as `blocked` with reason
// "stage_not_applicable_for_story_type" — saves the subagent token
// spend of discovering NA at runtime.
//
// Matrix (per (story_type × mode)):
//
//   pragmatic mode:
//     code   → hydrate, implement, code-review, commit
//     doc    → hydrate, implement, code-review, commit
//     mixed  → hydrate, implement, code-review, commit
//
//   strict mode:
//     code   → hydrate, atdd, implement, automate, test-review, code-review, commit
//     doc    → hydrate, implement, code-review, commit   (atdd / automate /
//              test-review skipped — no runtime behavior to test)
//     mixed  → hydrate, atdd, implement, automate, test-review, code-review, commit
//              (same as code; subagents return blocked-NA for portions if
//              applicable)
//
// All stages run in pragmatic regardless of type (pragmatic is already
// the lean pipeline). The matrix only diverges in strict.
func ApplicableStages(storyType state.StoryType, mode Mode) []state.Stage {
	if mode != ModeStrict {
		// Pragmatic is the same lean pipeline for every story_type.
		return pragmaticStages()
	}
	switch storyType {
	case state.StoryTypeDoc:
		return docStrictStages()
	default:
		// code, mixed, or unknown → full strict pipeline.
		return codeStrictStages()
	}
}

func pragmaticStages() []state.Stage {
	return []state.Stage{
		state.StageHydrate,
		state.StageImplement,
		state.StageCodeReview,
		state.StageCommit,
	}
}

func codeStrictStages() []state.Stage {
	return []state.Stage{
		state.StageHydrate,
		state.StageATDD,
		state.StageImplement,
		state.StageAutomate,
		state.StageTestReview,
		state.StageCodeReview,
		state.StageCommit,
	}
}

func docStrictStages() []state.Stage {
	return []state.Stage{
		state.StageHydrate,
		state.StageImplement,
		state.StageCodeReview,
		state.StageCommit,
	}
}

// SkippedStages returns the strict-mode stages OMITTED for the given
// story type (i.e., those the orchestrator should pre-record as
// blocked-NA). Empty for code/mixed; the three test-stages for doc.
// Always empty in pragmatic mode.
func SkippedStages(storyType state.StoryType, mode Mode) []state.Stage {
	if mode != ModeStrict {
		return nil
	}
	if storyType != state.StoryTypeDoc {
		return nil
	}
	return []state.Stage{
		state.StageATDD,
		state.StageAutomate,
		state.StageTestReview,
	}
}
