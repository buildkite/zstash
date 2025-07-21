package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/buildkite/zstash/internal/trace"
	"go.opentelemetry.io/otel/attribute"
)

// NscStore implements the Blob interface for NSC artifact storage which uses the nsc CLI tool
// https://namespace.so/docs/reference/cli/artifact-download
// https://namespace.so/docs/reference/cli/artifact-upload
type NscStore struct {
}

func New() *NscStore {
	return &NscStore{}
}

func (n *NscStore) Upload(ctx context.Context, filePath string, key string) (*TransferInfo, error) {
	_, span := trace.Start(ctx, "NscStore.Upload")
	defer span.End()

	start := time.Now()

	// Execute nsc artifact upload command
	result, err := runCommand(ctx, "", "nsc", "artifact", "upload", filePath, key)
	if err != nil {
		return nil, fmt.Errorf("failed to execute nsc upload command: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("nsc upload failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	// Get file size for transfer info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	duration := time.Since(start)
	bytesTransferred := fileInfo.Size()
	averageSpeed := calculateTransferSpeedMBps(bytesTransferred, duration)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesTransferred),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("nsc_key", key),
	)

	return &TransferInfo{
		BytesTransferred: bytesTransferred,
		TransferSpeed:    averageSpeed,
		RequestID:        "", // NSC doesn't expose request IDs
		Duration:         duration,
	}, nil
}

func (n *NscStore) Download(ctx context.Context, key string, filePath string) (*TransferInfo, error) {
	_, span := trace.Start(ctx, "NscStore.Download")
	defer span.End()

	start := time.Now()

	// Execute nsc artifact download command
	result, err := runCommand(ctx, "", "nsc", "artifact", "download", key, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to execute nsc download command: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("nsc download failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	// Get file size for transfer info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get downloaded file info: %w", err)
	}

	duration := time.Since(start)
	bytesTransferred := fileInfo.Size()
	averageSpeed := calculateTransferSpeedMBps(bytesTransferred, duration)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesTransferred),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("nsc_key", key),
	)

	return &TransferInfo{
		BytesTransferred: bytesTransferred,
		TransferSpeed:    averageSpeed,
		RequestID:        "", // NSC doesn't expose request IDs
		Duration:         duration,
	}, nil
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func runCommand(ctx context.Context, workingDir string, args ...string) (*CommandResult, error) {
	_, span := trace.Start(ctx, "runCommand")
	defer span.End()

	span.SetAttributes(attribute.StringSlice("command", args))

	cr := &CommandResult{}

	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ() // inherit the environment

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	err := cmd.Run()
	if err != nil {
		span.RecordError(err)
		if exitError, ok := err.(*exec.ExitError); ok {
			cr.ExitCode = exitError.ExitCode()
		} else {
			return nil, err
		}
	}

	cr.Stdout = stdout.String()
	cr.Stderr = stderr.String()

	return cr, nil
}
