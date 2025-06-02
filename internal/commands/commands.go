package commands

import (
	"fmt"
	"strings"

	"github.com/buildkite/zstash/internal/api"
)

type Globals struct {
	Debug   bool
	Version string
	Client  api.Client
}

func checkPath(path string) ([]string, error) {
	paths := strings.Fields(path)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths provided")
	}

	return paths, nil
}
