// Package assets owns the //go:embed declarations for the consumer-
// facing artifacts the `bmad install` subcommand ships into a target
// `.claude/` directory: the L3 agent definitions and the skill bundles
// (L1 bmad-v6-orchestrator + 7 helpers).
//
// # Why a dedicated package
//
// The //go:embed directive can only see files within or beneath the
// directory of the .go file that declares it. The shipped artifacts
// live at the repository root (`agents/`, `skills/`) — siblings of
// every Go package — so the embed lives in a sibling-level package
// (this one) that re-exposes the FSes to the rest of the codebase.
// This keeps the cmd package free of file-walking primitives that
// would otherwise leak into the install command.
//
// The trees deliberately retain their top-level names in the embedded
// FS roots:
//
//	agents/atdd-writer.md
//	agents/code-reviewer.md
//	...
//	skills/bmad-v6-orchestrator/SKILL.md
//	skills/port-pool/SKILL.md
//	...
//
// So the installer can `WalkDir` each FS from "." and use the walked
// path as the relative target on disk (e.g. `agents/atdd-writer.md`
// lands at `<target>/agents/atdd-writer.md`).
//
// # Drift guard
//
// The regression test in `assets/embed_test.go` enforces:
//   1. All 6 expected agent files are present in Agents.
//   2. All 8 expected skills (orchestrator + 7 helpers) are present
//      under Skills with at least a SKILL.md each.
//   3. Every helper-skill slug is referenced ≥ 1 time in the embedded
//      orchestrator SKILL.md. Without this guard, a future refactor
//      could re-orphan the helper skills the way they were dead-code
//      before issue #61 wired them into the L1 dispatch loop.
package assets

import "embed"

// Agents holds the L3 agent markdown files. Currently:
//   - atdd-writer
//   - code-reviewer
//   - story-hydrator
//   - tdd-implementer
//   - test-automate
//   - test-reviewer
//
//go:embed agents
var Agents embed.FS

// Skills holds the skill bundles. Currently:
//   - bmad-v6-orchestrator (L1 orchestrator)
//   - context-propagation, docker-up, healthcheck, port-pool,
//     sprint-planning, story-checkpoint, sweeper (7 helpers)
//
// Every skill is a directory containing at minimum SKILL.md; skill
// authors may include supporting files which the installer ships
// alongside the SKILL.md unchanged.
//
//go:embed skills
var Skills embed.FS
