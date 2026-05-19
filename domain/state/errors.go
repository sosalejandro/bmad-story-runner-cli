package state

import "errors"

// ErrNotFound is returned by Get-style methods when the requested row does not exist.
var ErrNotFound = errors.New("state: not found")

// ErrAlreadySet is returned by setter methods that refuse to clobber an
// already-populated column unless the caller explicitly opts in. Currently
// used by Stories.SetHydratedFile — issue #21 gap 2 — so a buggy
// orchestrator re-running `bmad dispatch record --stage hydrate --status
// ok --hydrated-file <other-path>` fails loudly instead of silently
// overwriting the prior hydrate output.
var ErrAlreadySet = errors.New("state: column already populated; pass overwrite=true to replace")
