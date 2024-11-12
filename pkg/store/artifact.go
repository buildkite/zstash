package store

import (
	"bytes"
	"context"
	"fmt"
	"log"
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

	// artifactName := filepath.Base(path)

	// buildkite-agent artifact search "key-whateer.tar.gz" -format "%p||%c||%s||%T\n"

	// using double pipe as a separator to avoid conflicts with file names and ;; as a separator for the lines as \n doesn't work
	result, err := runCommand(ctx, "", "buildkite-agent", "artifact", "search", remoteCacheURL, "-format", "%p||%c||%s||%T;;", "-allow-empty-results")
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

	dir := filepath.Dir(path)
	artifactName := filepath.Base(path)

	result, err := runCommand(ctx, dir, "buildkite-agent", "artifact", "upload", artifactName)
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

	// artifactName := filepath.Base(path)

	// buildkite-agent artifact download "key-whateer.tar.gz" .

	log.Printf("Downloading artifact %s to %s", remoteCacheURL, path)

	tempPath, err := os.MkdirTemp("/tmp", "buildkite-agent-artifact")
	if err != nil {
		return err
	}

	result, err := runCommand(ctx, tempPath, "buildkite-agent", "artifact", "download", remoteCacheURL, ".")
	if err != nil {
		return fmt.Errorf("error downloading artifact: %v", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("error downloading artifact: %s", result.Stderr)
	}

	fmt.Println(result.Stdout)

	log.Printf("Moving %s to %s\n", filepath.Join(tempPath, remoteCacheURL), path)

	err = os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return fmt.Errorf("error creating directory: %v", err)
	}

	// move the file to the correct path
	err = MoveFile(filepath.Join(tempPath, remoteCacheURL), path)
	if err != nil {
		return fmt.Errorf("error moving artifact: %v", err)
	}

	return nil
}

func parseSearchResult(stdout string) (string, bool, error) {
	lines := strings.Split(stdout, ";;")

	log.Printf("lines count: %d data: %v\n", len(lines), lines)

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
