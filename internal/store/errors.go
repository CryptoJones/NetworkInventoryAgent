package store

import "errors"

var (
	ErrNotFound  = errors.New("record not found")
	ErrDuplicate = errors.New("record already exists")
)
