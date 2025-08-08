package commands

import (
	"math"

	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/cache"
	"github.com/buildkite/zstash/internal/console"
)

type CommonFlags struct {
	Organization string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG"`
	Branch       string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH"`
	Pipeline     string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG"`
	BucketURL    string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Format       string `flag:"format" help:"The format of the archive to use." enum:"zip" default:"zip" env:"BUILDKITE_CACHE_FORMAT"`
}

type Globals struct {
	Caches   []cache.Cache
	Common   CommonFlags
	Debug    bool
	Endpoint string
	Printer  *console.Printer
	Version  string
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
