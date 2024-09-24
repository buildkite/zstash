package store

import (
	"fmt"
)

var (
	// ErrNotFound is returned when the requested object is not found.
	ErrNotFound = fmt.Errorf("cache key not found in remote cache")
)
