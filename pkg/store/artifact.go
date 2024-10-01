package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/buildkite/zstash/internal/trace"
)

type ArtifactStore struct {
}

func NewArtifactStore() (*ArtifactStore, error) {
	return &ArtifactStore{}, nil
}

func (s *ArtifactStore) Exists(ctx context.Context, remoteCacheURL, path string) (string, bool, error) {
	ctx, span := trace.Start(ctx, "ArtifactStore.Exists")
	defer span.End()

	artifactName := filepath.Base(path)

	// buildkite-agent artifact search "key-whateer.tar.gz" -format "%p||%c||%s||%T\n"

	// using double pipe as a separator to avoid conflicts with file names and ;; as a separator for the lines as \n doesn't work
	result, err := runCommand(ctx, "buildkite-agent", "artifact", "search", artifactName, "-format", "%p||%c||%s||%T;;")
	if err != nil {
		return "", false, fmt.Errorf("error searching artifact: %v", err)
	}

	if result.ExitCode != 0 {
		return "", false, fmt.Errorf("error searching artifact: %s", result.Stderr)
	}

	return parseSearchResult(result.Stdout)
}

func (s *ArtifactStore) Upload(ctx context.Context, remoteCacheURL, path, sha256sum string, expiresInSecs int64) error {
	ctx, span := trace.Start(ctx, "ArtifactStore.Upload")
	defer span.End()

	// buildkite-agent upload "key-whateer.tar.gz"

	result, err := runCommand(ctx, "buildkite-agent", "artifact", "upload", path)
	if err != nil {
		return fmt.Errorf("error uploading artifact: %v", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("error uploading artifact: %s", result.Stderr)
	}

	fmt.Println(result.Stdout)

	return nil
}

func (s *ArtifactStore) Download(ctx context.Context, remoteCacheURL, path, sha256sum string) error {
	ctx, span := trace.Start(ctx, "ArtifactStore.Download")
	defer span.End()

	artifactName := filepath.Base(path)

	// buildkite-agent artifact download "key-whateer.tar.gz" .

	tempPath, err := os.MkdirTemp("tmp", "buildkite-agent-artifact")
	if err != nil {
		return err
	}

	result, err := runCommand(ctx, "buildkite-agent", "artifact", "download", artifactName, tempPath)
	if err != nil {
		return fmt.Errorf("error downloading artifact: %v", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("error downloading artifact: %s", result.Stderr)
	}

	fmt.Println(result.Stdout)

	// move the file to the correct path
	err = os.Rename(filepath.Join(tempPath, artifactName), path)
	if err != nil {
		return fmt.Errorf("error moving artifact: %v", err)
	}

	return nil
}

func parseSearchResult(stdout string) (string, bool, error) {
	lines := strings.Split(stdout, ";;")

	if len(lines) == 0 {
		return "", false, nil
	}

	tokens := strings.Split(lines[0], "||")

	if len(tokens) == 4 {
		return tokens[3], true, nil
	}

	return "", false, nil
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func runCommand(ctx context.Context, args ...string) (*CommandResult, error) {
	_, span := trace.Start(ctx, "runCommand")
	defer span.End()

	span.SetAttributes(attribute.StringSlice("command", args))

	cr := &CommandResult{}

	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ() // inherit the environment

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
