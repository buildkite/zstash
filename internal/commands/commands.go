package commands

import (
	"fmt"
	"math"
	"strings"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/console"
	"github.com/buildkite/zstash/internal/key"
	"github.com/rs/zerolog/log"
)

type CommonFlags struct {
	Organization string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG"`
	Branch       string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH"`
	Pipeline     string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG"`
	BucketURL    string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix       string `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
	Format       string `flag:"format" help:"The format of the archive to use." enum:"zip" default:"zip" env:"BUILDKITE_CACHE_FORMAT"`
}

type Globals struct {
	Debug   bool
	Version string
	Client  api.Client
	Printer *console.Printer
	Caches  []Cache
	Common  CommonFlags
}

// templatedPaths validates the provided path and returns a list of paths that have been templated.
func templatedPaths(id string, pathsTemplates []string) ([]string, error) {
	if len(pathsTemplates) == 0 {
		return nil, fmt.Errorf("no paths provided")
	}

	resolvedPaths := make([]string, len(pathsTemplates))

	log.Debug().Str("id", id).Strs("paths_templates", pathsTemplates).Msg("templating paths")

	for n, pathTemplate := range pathsTemplates {

		// trim quotes and whitespace
		resolvedPathTemplate := strings.Trim(pathTemplate, "\"' \t")

		log.Debug().Str("path_template", resolvedPathTemplate).Msg("templating path")

		resolvedPath, err := key.Template(id, resolvedPathTemplate, false)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve templated path %q: %w", pathTemplate, err)
		}

		log.Debug().Str("resolved_path", resolvedPath).Msg("resolved path")

		resolvedPaths[n] = resolvedPath
	}

	return resolvedPaths, nil
}

// restoreKeys generates a list of restore keys from the provided ID and restore key list.
func restoreKeys(id string, restoreKeyTemplates []string) ([]string, error) {
	restoreKeys := make([]string, len(restoreKeyTemplates))

	log.Debug().Str("id", id).Strs("restore_keys", restoreKeyTemplates).Msg("templating restore keys")

	for n, restoreKeyTemplate := range restoreKeyTemplates {

		// trim quotes and whitespace
		restoreKeyTemplate = strings.Trim(restoreKeyTemplate, "\"' \t")

		log.Debug().Str("restore_key_template", restoreKeyTemplate).Msg("templating restore key")

		restoreKey, err := key.Template(id, restoreKeyTemplate, false)
		if err != nil {
			return nil, fmt.Errorf("failed to template restore key: %w", err)
		}

		log.Debug().Str("restore_key", restoreKey).Msg("templated restore key")

		restoreKeys[n] = restoreKey
	}

	return restoreKeys, nil
}

// calculate the compression ratio
func compressionRatio(archiveInfo *archive.ArchiveInfo) float64 {
	if archiveInfo.Size == 0 {
		return 0.0
	}
	return float64(archiveInfo.WrittenBytes) / float64(archiveInfo.Size)
}

// Int64ToUint64 converts an int64 to uint64, handling negative values and max int64
func Int64ToUint64(x int64) uint64 {
	if x < 0 {
		return 0
	}
	if x == math.MaxInt64 {
		return math.MaxUint64
	}
	return uint64(x)
}
