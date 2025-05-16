package commands

import "github.com/buildkite/zstash/internal/api"

type Globals struct {
	Debug   bool
	Version string
	Client  api.Client
}
