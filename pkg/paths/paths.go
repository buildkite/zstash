package paths

import (
	"path/filepath"
	"strings"
)

// RelPathCheck returns the relative path if the path is within the base path.
func RelPathCheck(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return ""
	}

	if strings.HasPrefix(rel, "..") {
		return ""
	}

	return rel
}
