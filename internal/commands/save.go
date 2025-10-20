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

type SaveCmd struct {
	Ids []string `flag:"ids" help:"List of comma delimited cache IDs to save, defaults to all." env:"BUILDKITE_CACHE_IDS"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running SaveCmd")

	// Get list of all caches
	caches := globals.CacheClient.ListCaches()

	// Filter by IDs if specified
	var cacheIDs []string
	if len(cmd.Ids) > 0 {
		cacheIDs = cmd.Ids
	} else {
		// Save all caches
		for _, c := range caches {
			cacheIDs = append(cacheIDs, c.ID)
		}
	}

	// Save each cache
	for _, cacheID := range cacheIDs {
		span.SetAttributes(attribute.String("cache_id", cacheID))

		if err := cmd.saveCache(ctx, cacheID, globals.CacheClient, globals); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *SaveCmd) saveCache(ctx context.Context, cacheID string, cacheClient *zstash.Cache, globals *Globals) error {
	ctx, span := trace.Start(ctx, "saveCache")
	defer span.End()

	// Get cache config for logging
	cacheConfig, err := cacheClient.GetCache(cacheID)
	if err != nil {
		return trace.NewError(span, "failed to get cache: %w", err)
	}

	span.SetAttributes(
		attribute.String("id", cacheConfig.ID),
		attribute.String("key", cacheConfig.Key),
		attribute.String("registry", cacheConfig.Registry),
		attribute.StringSlice("fallback_keys", cacheConfig.FallbackKeys),
		attribute.StringSlice("paths", cacheConfig.Paths),
	)

	registry := cacheConfig.Registry
	if registry == "" {
		registry = "~"
	}

	globals.Printer.Info("💾", "Starting cache save for Registry: %s ID: %s", registry, cacheConfig.ID)

	// Call library save method
	result, err := cacheClient.Save(ctx, cacheID)
	if err != nil {
		globals.Printer.Error("❌", "Cache save failed: %s", err)
		return trace.NewError(span, "failed to save cache: %w", err)
	}

	// Check for result error
	if result.Error != nil {
		globals.Printer.Error("❌", "Cache save failed for ID %s: %s", cacheID, result.Error)
		return result.Error
	}

	// Handle cache already exists
	if !result.CacheCreated {
		globals.Printer.Success("✅", "Cache already exists for key: %s", result.Key)
		fmt.Println("true") // write to stdout
		return nil
	}

	// Log success
	log.Info().
		Str("key", result.Key).
		Int64("size", result.Archive.Size).
		Str("sha256sum", result.Archive.Sha256Sum).
		Dur("duration_ms", result.Archive.Duration).
		Int64("entries", result.Archive.WrittenEntries).
		Int64("bytes_written", result.Archive.WrittenBytes).
		Float64("compression_ratio", result.Archive.CompressionRatio).
		Msg("archive built")

	globals.Printer.Success("🎉", "Cache saved successfully")

	// Print summary table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", result.Key).
		Row("Archive Size", humanize.Bytes(Int64ToUint64(result.Archive.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(result.Archive.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", result.Archive.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", result.Archive.CompressionRatio)).
		Row("Build Duration", result.Archive.Duration.String())

	if result.Transfer != nil {
		t = t.
			Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", result.Transfer.TransferSpeed)).
			Row("Upload Duration", result.Transfer.Duration.String())
	}

	t = t.Row("Paths", strings.Join(result.Archive.Paths, ", "))

	globals.Printer.Info("📊", "Cache save summary:\n%s", t.Render())

	fmt.Println("true") // write to stdout

	return nil
}
