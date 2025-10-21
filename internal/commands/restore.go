package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/buildkite/zstash"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/dustin/go-humanize"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

const (
	CacheRestoreHit  = "true"
	CacheRestoreMiss = "false"
)

type RestoreCmd struct {
	Ids []string `flag:"ids" help:"List of comma delimited cache IDs to restore, defaults to all." env:"BUILDKITE_CACHE_IDS"`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running RestoreCmd")

	// Get list of all caches
	caches := globals.CacheClient.ListCaches()

	// Filter by IDs if specified
	var cacheIDs []string
	if len(cmd.Ids) > 0 {
		cacheIDs = cmd.Ids
	} else {
		// Restore all caches
		for _, c := range caches {
			cacheIDs = append(cacheIDs, c.ID)
		}
	}

	// Restore each cache
	for _, cacheID := range cacheIDs {
		span.SetAttributes(attribute.String("cache_id", cacheID))

		if err := cmd.restoreCache(ctx, cacheID, globals.CacheClient, globals); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *RestoreCmd) restoreCache(ctx context.Context, cacheID string, cacheClient *zstash.Cache, globals *Globals) error {
	ctx, span := trace.Start(ctx, "restoreCache")
	defer span.End()

	// Get cache config for logging
	cacheConfig, err := cacheClient.GetCache(cacheID)
	if err != nil {
		return trace.NewError(span, "failed to get cache: %w", err)
	}

	span.SetAttributes(
		attribute.String("key", cacheConfig.Key),
		attribute.StringSlice("paths", cacheConfig.Paths),
		attribute.StringSlice("fallback_keys", cacheConfig.FallbackKeys),
	)

	registry := cacheConfig.Registry
	if registry == "" {
		registry = "~"
	}

	globals.Printer.Info("♻️", "Starting restore for Registry: %s ID: %s", registry, cacheConfig.ID)

	// Call library restore method
	result, err := cacheClient.Restore(ctx, cacheID)
	if err != nil {
		globals.Printer.Error("❌", "Cache restore failed: %s", err)
		return trace.NewError(span, "failed to restore cache: %w", err)
	}

	// Handle cache miss
	if !result.CacheRestored {
		globals.Printer.Warn("💨", "Cache miss for key: %s", result.Key)
		fmt.Println(CacheRestoreMiss)
		return nil
	}

	// Handle cache hit or fallback
	if result.FallbackUsed {
		globals.Printer.Warn("⚠️", "Using fallback cache for key: %s", result.Key)
	} else {
		globals.Printer.Info("✅", "Cache hit for key: %s", result.Key)
	}

	globals.Printer.Success("🗜️", "Extracted %d files from cache archive for paths: %v",
		result.Archive.WrittenEntries, result.Archive.Paths)

	// Log debug info
	log.Debug().
		Int64("size", result.Archive.Size).
		Dur("duration_ms", result.Archive.Duration).
		Int64("written_bytes", result.Archive.WrittenBytes).
		Int64("written_entries", result.Archive.WrittenEntries).
		Float64("compression_ratio", result.Archive.CompressionRatio).
		Msg("archive extracted")

	// Print summary table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", result.Key).
		Row("Size", humanize.Bytes(Int64ToUint64(result.Archive.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(result.Archive.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", result.Archive.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", result.Archive.CompressionRatio)).
		Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", result.Transfer.TransferSpeed)).
		Row("Duration", result.Archive.Duration.String()).
		Row("Paths", strings.Join(result.Archive.Paths, ", "))

	globals.Printer.Info("📊", "Cache restore summary:\n%s", t.Render())

	// Output result to stdout
	if result.FallbackUsed {
		fmt.Println(CacheRestoreMiss)
	} else {
		fmt.Println(CacheRestoreHit)
	}

	return nil
}
