// Package assets regression tests.
//
// These tests are the *contract guard* for what bmad-cli ships to
// consumers via `bmad install`. They fail loudly when:
//
//   1. An expected L3 agent file is missing from the embedded tree
//      (someone deleted or renamed a file in agents/ without
//      updating callers).
//   2. An expected skill directory is missing from the embedded tree
//      (same drift class as agents — a skill went missing).
//   3. The L1 orchestrator SKILL.md does NOT reference one of the 7
//      helper skill slugs by name. This guards against the original
//      dead-code situation (issue #61): the 7 helper skills shipped
//      but the orchestrator never invoked them, so they were unreachable
//      in practice. A future refactor that re-orphans a helper skill
//      must update the orchestrator AT THE SAME TIME.
//
// If you find yourself wanting to "loosen" assertion 3, the right answer
// is almost never "delete the assertion" — it's "remove the helper skill
// from the embedded set entirely if it's truly no longer needed". The
// guard is the cheap way to keep wired/unwired honest.
package assets

import (
	"io/fs"
	"strings"
	"testing"
)

// expectedAgentFiles is the canonical L3 agent set. Adding a new agent
// requires updating BOTH this list AND the test in cmd/install_test.go —
// the duplication is intentional: each test owns its own contract.
var expectedAgentFiles = []string{
	"agents/atdd-writer.md",
	"agents/code-reviewer.md",
	"agents/story-hydrator.md",
	"agents/tdd-implementer.md",
	"agents/test-automate.md",
	"agents/test-reviewer.md",
}

// expectedSkillDirs is the canonical skill set: the L1 orchestrator
// plus 7 helpers. Same duplication rationale as expectedAgentFiles.
var expectedSkillDirs = []string{
	"skills/bmad-v6-orchestrator",
	"skills/context-propagation",
	"skills/docker-up",
	"skills/healthcheck",
	"skills/port-pool",
	"skills/sprint-planning",
	"skills/story-checkpoint",
	"skills/sweeper",
}

// helperSkillSlugs is the set of helper skills whose names MUST appear
// in the orchestrator SKILL.md. The orchestrator skill itself is excluded
// (it would self-reference). The 7 helpers are the ones the dispatch
// loop needs to invoke by slug.
var helperSkillSlugs = []string{
	"context-propagation",
	"docker-up",
	"healthcheck",
	"port-pool",
	"sprint-planning",
	"story-checkpoint",
	"sweeper",
}

// TestEmbed_AgentsPresent verifies every expected agent file exists in
// the embedded FS.
func TestEmbed_AgentsPresent(t *testing.T) {
	t.Parallel()
	for _, path := range expectedAgentFiles {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			b, err := fs.ReadFile(Agents, path)
			if err != nil {
				t.Fatalf("embedded agent file %s missing: %v", path, err)
			}
			if len(b) == 0 {
				t.Errorf("embedded agent file %s is empty — likely lost content", path)
			}
		})
	}
}

// TestEmbed_SkillsPresent verifies every expected skill directory has
// a non-empty SKILL.md.
func TestEmbed_SkillsPresent(t *testing.T) {
	t.Parallel()
	for _, dir := range expectedSkillDirs {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			skillPath := dir + "/SKILL.md"
			b, err := fs.ReadFile(Skills, skillPath)
			if err != nil {
				t.Fatalf("embedded skill %s missing: %v", skillPath, err)
			}
			if len(b) == 0 {
				t.Errorf("embedded skill %s is empty", skillPath)
			}
		})
	}
}

// TestEmbed_OrchestratorReferencesEveryHelperSkill is the dead-code
// guard from issue #61. The orchestrator SKILL.md must mention every
// helper skill slug by name at least once — if it doesn't, the helper
// is orphaned regardless of whether it's embedded.
//
// We grep for the literal slug string. False positives (e.g. a slug
// substring appearing in unrelated prose) are vanishingly unlikely
// because the slugs are deliberately specific compound words.
func TestEmbed_OrchestratorReferencesEveryHelperSkill(t *testing.T) {
	t.Parallel()
	const orchPath = "skills/bmad-v6-orchestrator/SKILL.md"
	body, err := fs.ReadFile(Skills, orchPath)
	if err != nil {
		t.Fatalf("read orchestrator SKILL.md: %v", err)
	}
	text := string(body)

	for _, slug := range helperSkillSlugs {
		t.Run(slug, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(text, slug) {
				t.Errorf("orchestrator SKILL.md does not reference helper skill %q — "+
					"this regression guard means a helper skill is dead code from "+
					"L1's perspective. Either wire it into the dispatch loop or "+
					"remove it from the embedded set entirely.", slug)
			}
		})
	}
}

// TestEmbed_NoUnexpectedTopLevelAgents catches the inverse of
// TestEmbed_AgentsPresent: if a new agent file lands in assets/agents
// without being added to expectedAgentFiles, this test surfaces it.
// We want explicit acknowledgement of every shipped artifact, not
// silent drift where the install command ships files nobody enumerated.
func TestEmbed_NoUnexpectedTopLevelAgents(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(Agents, "agents")
	if err != nil {
		t.Fatalf("read embedded agents/: %v", err)
	}
	expected := map[string]bool{}
	for _, p := range expectedAgentFiles {
		// trim "agents/" prefix
		expected[p[len("agents/"):]] = true
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("unexpected directory in agents/: %s (only flat .md files supported)", e.Name())
			continue
		}
		if !expected[e.Name()] {
			t.Errorf("unexpected embedded agent file: agents/%s — "+
				"add it to expectedAgentFiles in this test if it is intended to ship", e.Name())
		}
	}
}

// TestEmbed_NoUnexpectedTopLevelSkills catches the inverse of
// TestEmbed_SkillsPresent: an unenumerated skill directory at the
// skills/ root is a contract drift.
func TestEmbed_NoUnexpectedTopLevelSkills(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(Skills, "skills")
	if err != nil {
		t.Fatalf("read embedded skills/: %v", err)
	}
	expected := map[string]bool{}
	for _, p := range expectedSkillDirs {
		expected[p[len("skills/"):]] = true
	}
	for _, e := range entries {
		// Files at the skills/ root (e.g. README.md) are developer-facing
		// and filtered by the install command — not a contract violation.
		if !e.IsDir() {
			continue
		}
		if !expected[e.Name()] {
			t.Errorf("unexpected embedded skill directory: skills/%s — "+
				"add it to expectedSkillDirs in this test if it is intended to ship", e.Name())
		}
	}
}
