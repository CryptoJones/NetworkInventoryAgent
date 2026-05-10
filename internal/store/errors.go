package store

import "errors"

// ErrNotFound is returned by Get methods when no record matches the query.
var ErrNotFound = errors.New("record not found")
