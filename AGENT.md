# Agent Guidelines for zstash

This file contains information to help AI coding agents work effectively with the zstash codebase.

## Project Overview

**zstash** is a cache save and restore tool for Buildkite that manages cache archives to/from cloud storage. It's written in Go and supports multiple storage backends including S3, NSC (Namespace), and local file storage.

- **Repository**: https://github.com/buildkite/zstash
- **Language**: Go 1.24+
- **License**: MIT

## Common Commands

### Building and Testing

```bash
# Run all tests with coverage
make test
# or directly:
go test -coverprofile coverage.out -covermode atomic -v ./...

# Run specific package tests
go test ./store -v
go test ./store -v -run TestLocalFileBlob

# Build snapshot (single target)
make snapshot
# Uses goreleaser
```

### Linting and Formatting

```bash
# Run linter (golangci-lint)
make lint

# Run linter with auto-fix
make lint-fix

# Direct command:
golangci-lint run ./...
golangci-lint run --fix ./...
```

### Deployment (AWS SAM)

```bash
# Deploy cache bucket stack (dev)
make cache-bucket
```

## Project Structure

```
.
├── api/              # Buildkite API client
├── archive/          # Archive creation and extraction (zip, tar.zst)
├── cache/            # Cache configuration and template expansion
├── configuration/    # Configuration types
├── internal/
│   ├── key/         # Cache key generation (checksums, templates)
│   └── trace/       # OpenTelemetry tracing utilities
├── store/           # Storage backends (S3, NSC, local file)
├── specs/           # Design specifications
├── sam/             # AWS SAM templates
├── .buildkite/      # CI/CD configuration
├── cache.go         # Main cache client implementation
├── save.go          # Cache save operations
├── restore.go       # Cache restore operations
└── zstash.go        # Package entry point and docs
```

## Code Conventions

### General Go Practices

- **Package documentation**: All packages have doc comments in their main file
- **Function documentation**: Exported functions have doc comments
- **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for error wrapping
- **Context**: Pass `context.Context` as first parameter
- **Struct initialization**: Use named fields in struct literals

### Testing

- **Framework**: `github.com/stretchr/testify` (assert, require)
- **Table-driven tests**: Most tests use table-driven approach
- **Test organization**: Tests are in `*_test.go` files alongside implementation
- **Coverage**: Tests should include coverage for error cases
- **Test helpers**: Use `t.TempDir()` for temporary directories
- **Validation tests**: Security-sensitive code (like path validation) has comprehensive test coverage

### Store Package Patterns

When implementing new storage backends (like the recent local file store):

1. **Interface**: Implement the `Blob` interface from [store/blob.go](file:///Users/markw/Code/work/zstash/store/blob.go):
   ```go
   type Blob interface {
       Upload(ctx context.Context, filePath string, key string) (*TransferInfo, error)
       Download(ctx context.Context, key string, destPath string) (*TransferInfo, error)
   }
   ```

2. **Atomic operations**: Use temp file + fsync + rename pattern for atomicity
3. **Validation**: Strict input validation (see `validateKey`, `validateFilePath`)
4. **Metrics**: Return `TransferInfo` with bytes transferred, speed, duration
5. **Tracing**: Use `trace.Start(ctx, "Operation")` with attributes
6. **Constants**: Add store type to [store/store.go](file:///Users/markw/Code/work/zstash/store/store.go) constants

### Security

- **Path validation**: Prevent path traversal attacks (see `validateKey` in store implementations)
- **Command injection**: Validate inputs before exec (see NSC store)
- **Secrets**: Never log or commit secrets
- **File permissions**: Use `0o755` for directories, `0o644` for files

## Storage Backends

### Supported Types

1. **S3** (`local_s3`): AWS S3 or S3-compatible storage
   - URL format: `s3://bucket-name?region=us-east-1&endpoint=http://localhost:9000`
   - Implementation: [store/s3.go](file:///Users/markw/Code/work/zstash/store/s3.go)

2. **NSC** (`local_hosted_agents`): Namespace artifact storage
   - Uses `nsc` CLI tool
   - Implementation: [store/nsc.go](file:///Users/markw/Code/work/zstash/store/nsc.go)

3. **Local File** (`local_file`): Local filesystem storage
   - URL format: `file:///path/to/cache` or `file://~/.buildkitecache`
   - Implementation: [store/file.go](file:///Users/markw/Code/work/zstash/store/file.go)
   - Design: [specs/local_file_storage.md](file:///Users/markw/Code/work/zstash/specs/local_file_storage.md)

### Adding New Storage Backend

See the local file store implementation as a reference:
1. Add constant to `store/store.go`
2. Update `IsValidStore()` function
3. Create `store/<name>.go` implementing `Blob` interface
4. Update `NewBlobStore()` factory in `store/blob.go`
5. Create `store/<name>_test.go` with comprehensive tests
6. Add design spec to `specs/` directory

## Archive Formats

- **ZIP**: Default format
- **tar.zst**: Zstandard-compressed tar (better compression)

Format selection is controlled by the `--format` flag or `BUILDKITE_CACHE_FORMAT` environment variable.

## Tracing

OpenTelemetry tracing is supported:

```bash
# Enable OTLP gRPC tracing (e.g., Honeycomb)
export TRACE_EXPORTER=grpc
export OTEL_SERVICE_NAME=zstash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://api.honeycomb.io:443
export OTEL_EXPORTER_OTLP_HEADERS=x-honeycomb-team=API_TOKEN,x-honeycomb-dataset=dev
```

Default is `noop` (no tracing).

## Dependencies

Key dependencies:
- `github.com/aws/aws-sdk-go-v2` - AWS SDK
- `github.com/stretchr/testify` - Testing assertions
- `github.com/klauspost/compress` - Zstandard compression
- `github.com/wolfeidau/quickzip` - Fast zip operations
- `drjosh.dev/zzglob` - Glob pattern matching
- `go.opentelemetry.io/otel` - Tracing

## Environment Variables

Common environment variables:
- `BUILDKITE_AGENT_ACCESS_TOKEN` - API token
- `BUILDKITE_CACHE_BUCKET_URL` - Storage backend URL
- `BUILDKITE_CACHE_FORMAT` - Archive format (zip/tar.zst)
- `BUILDKITE_CACHE_PREFIX` - Key prefix
- `BUILDKITE_ORGANIZATION_SLUG` - Organization
- `BUILDKITE_PIPELINE_SLUG` - Pipeline
- `BUILDKITE_BRANCH` - Branch name
- `BUILDKITE_ZSTASH_DEBUG` - Enable debug logging
- `BUILDKITE_ZSTASH_TRACE_EXPORTER` - Trace exporter type

## Common Tasks

### Running Specific Tests

```bash
# Run all store tests
go test ./store -v

# Run specific test
go test ./store -v -run TestLocalFileBlobUploadDownload

# Run with coverage
go test ./store -coverprofile=coverage.out -covermode=atomic
```

### Checking Code Quality

```bash
# Lint the entire codebase
make lint

# Auto-fix linting issues
make lint-fix

# Check for security issues (gosec)
golangci-lint run --enable gosec ./...
```

### Adding New Features

1. Check for existing patterns in similar code
2. Add comprehensive tests (success and error cases)
3. Update relevant documentation
4. Run linter and tests before committing
5. Add design spec to `specs/` if it's a significant feature

## Gotchas

- **Path separators**: Use `filepath.Join()` for cross-platform compatibility
- **URL parsing**: Use `url.Parse()` and handle scheme validation
- **Tilde expansion**: Must manually expand `~` with `os.UserHomeDir()`
- **Windows**: Rename semantics differ; remove destination before rename
- **Temp files**: Always clean up with deferred remove on error

## Getting Help

- Check existing implementations (especially store package for similar patterns)
- Review test files for usage examples
- Read package documentation in source files
- Check specs/ directory for design documents
