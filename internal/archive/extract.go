package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zip"
	"github.com/wolfeidau/quickzip"
	"go.opentelemetry.io/otel/attribute"

	"github.com/buildkite/zstash/internal/trace"
)

func ExtractFiles(ctx context.Context, zipFile *os.File, zipFileLen int64, paths []string) (*ArchiveInfo, error) {
	_, span := trace.Start(ctx, "ExtractFiles")
	defer span.End()

	start := time.Now()

	extract, err := quickzip.NewExtractorFromReader(zipFile, zipFileLen)
	if err != nil {
		return nil, fmt.Errorf("failed to create extractor: %w", err)
	}

	mappings, err := PathsToMappings(paths)
	if err != nil {
		return nil, fmt.Errorf("failed to create mappings: %w", err)
	}

	err = extract.ExtractWithPathMapper(ctx, func(file *zip.File) (string, error) {
		for _, mapping := range mappings {
			if strings.HasPrefix(file.Name, mapping.RelativePath) {
				return filepath.Join(mapping.Chroot, file.Name), nil
			}
		}

		return "", fmt.Errorf("failed to find path mapping for: %s", file.Name)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to extract zip file: %w", err)
	}

	bytesExtracted, countExtracted := extract.Written()

	span.SetAttributes(
		attribute.Int64("zipFileLen", zipFileLen),
		attribute.Int64("fileExtracted", countExtracted),
		attribute.Int64("bytesExtracted", bytesExtracted),
	)

	return &ArchiveInfo{
		ArchivePath:    zipFile.Name(),
		Size:           zipFileLen,
		WrittenBytes:   bytesExtracted,
		WrittenEntries: countExtracted,
		Duration:       time.Since(start),
	}, nil
}
