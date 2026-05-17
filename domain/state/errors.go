package state

import "errors"

// ErrNotFound is returned by Get-style methods when the requested row does not exist.
var ErrNotFound = errors.New("state: not found")
