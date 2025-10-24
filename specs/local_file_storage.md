# Local File Storage Implementation Plan

## Overview

Implement a local file store for zstash that accepts URLs like `file://~/.buildkitecache` to store cache files and their attributes on the local filesystem. The store will implement the `Blob` interface with `Upload` and `Download` methods, following patterns from existing S3 and NSC implementations.

## Storage Structure

### File Layout

Keys map directly to file paths under the root directory, maintaining the key's hierarchical structure:

```
~/.buildkitecache/                    # root directory
  └── caches/                         # directory from key
      └── linux/                      # directory from key
          ├── x.y.tar.gz              # actual cache file
          └── x.y.tar.gz.attrs.json   # metadata sidecar
```

**Mapping rules:**
- Key: `caches/linux/x.y.tar.gz`
- Data file: `<root>/caches/linux/x.y.tar.gz`
- Metadata file: `<root>/caches/linux/x.y.tar.gz.attrs.json`

### Metadata Format

Each data file has an accompanying JSON sidecar file (`.attrs.json`) containing metadata:

```json
{
  "key": "caches/linux/x.y.tar.gz",
  "size": 12345678,
  "mod_time": "2025-10-24T10:30:00Z",
  "mode": "0644",
  "sha256": "abc123...",
  "created_at": "2025-10-24T10:30:00Z",
  "version": 1
}
```

**Field descriptions:**
- `key`: Original key used for storage
- `size`: File size in bytes
- `mod_time`: File modification time (RFC3339Nano format)
- `mode`: File permissions (octal string)
- `sha256`: Optional checksum (computed during write if feasible)
- `created_at`: Timestamp when file was stored
- `version`: Metadata schema version (currently 1)

**Rationale for JSON sidecar:**
- Portable across operating systems
- More reliable than extended file attributes (which may be disabled or unsupported)
- Transparent and easy to inspect/debug
- Simplifies cleanup (delete both files together)

## Implementation Details

### 1. Constants and Types

**store/store.go:**
```go
const LocalFileStore = "local_file"
```

Update `IsValidStore()` to include `LocalFileStore`.

### 2. File Structure

**store/file.go (new file):**

```go
type LocalFileBlob struct {
    root string  // Absolute path to root directory
}

type FileMetadata struct {
    Key       string `json:"key"`
    Size      int64  `json:"size"`
    ModTime   string `json:"mod_time"`
    Mode      string `json:"mode"`
    SHA256    string `json:"sha256,omitempty"`
    CreatedAt string `json:"created_at"`
    Version   int    `json:"version"`
}
```

### 3. URL Parsing

**Function:** `NewLocalFileBlob(ctx context.Context, fileURL string) (*LocalFileBlob, error)`

**Steps:**
1. Parse URL with `url.Parse()`
2. Validate scheme is `file`
3. Extract path from `u.Path` (allow empty host or "localhost")
4. Handle `~` expansion:
   - If path starts with `~` or `~/`, expand using `os.UserHomeDir()`
5. Convert to OS path: `filepath.FromSlash()` then `filepath.Clean()`
6. Validate root is not empty or `/`
7. Create root directory: `os.MkdirAll(root, 0o755)`
8. Return `&LocalFileBlob{root: root}`

**URL examples:**
- `file://~/.buildkitecache`
- `file:///absolute/path/to/cache`
- `file://localhost/var/cache`

### 4. Key Validation and Path Safety

**Function:** `validateKey(key string) error`

**Validation rules:**
- Non-empty
- Length <= 256 characters (or 512 for longer paths)
- Allowed characters: `[a-zA-Z0-9._/-]`
- Reject patterns: `../`, `/./`, `//`, `&&`, `||`, `;`, `` ` ``, `$`
- Reject absolute paths
- Reject Windows drive letters

**Function:** `keyToPaths(key string) (dataPath, metaPath string, error)`

**Steps:**
1. Strip leading slash: `k := strings.TrimPrefix(key, "/")`
2. Clean and normalize: `k = filepath.Clean(filepath.FromSlash(k))`
3. Reject if `k == "."` or `k == ""`
4. Join with root: `dataPath := filepath.Join(root, k)`
5. **Containment check** (prevent traversal):
   ```go
   rel, _ := filepath.Rel(root, dataPath)
   if strings.HasPrefix(rel, "..") {
       return error
   }
   ```
6. Metadata path: `metaPath := dataPath + ".attrs.json"`
7. Create parent directories: `os.MkdirAll(filepath.Dir(dataPath), 0o755)`

### 5. Upload Implementation

**Function:** `Upload(ctx context.Context, srcPath string, key string) (*TransferInfo, error)`

**Algorithm:**
1. Start trace: `trace.Start(ctx, "LocalFileBlob.Upload")`
2. Validate `srcPath` and `key`
3. Open source file and stat for size
4. Resolve `dataPath` and `metaPath` using `keyToPaths()`
5. Create parent directory
6. **Atomic write** (temp file + rename):
   - Generate temp path: `tmpData := dataPath + fmt.Sprintf(".tmp-%d-%x", os.Getpid(), rand)`
   - Create temp file with `O_CREATE|O_EXCL`, mode `0o644`
   - Copy data with `io.Copy()` (optionally hash with `io.TeeReader`)
   - Fsync temp file and close
   - On Windows: `os.Remove(dataPath)` if exists (before rename)
   - Rename: `os.Rename(tmpData, dataPath)`
   - Fsync parent directory (optional but recommended)
7. Write metadata:
   - Build `FileMetadata` from source file info
   - Generate temp metadata path
   - Write JSON to temp file, fsync, close
   - Rename to final metadata path
   - Fsync parent directory
8. Calculate metrics:
   - `BytesTransferred = srcInfo.Size()`
   - `TransferSpeed` via `calculateTransferSpeedMBps()`
   - `RequestID = ""` (empty like NSC)
9. Add trace attributes: `bytes_transferred`, `transfer_speed`, `key`
10. Return `TransferInfo`

**Error handling:**
- Clean up temp files on error (defer remove)
- Wrap errors with context: `fmt.Errorf("failed to upload %s: %w", key, err)`

### 6. Download Implementation

**Function:** `Download(ctx context.Context, key string, destPath string) (*TransferInfo, error)`

**Algorithm:**
1. Start trace: `trace.Start(ctx, "LocalFileBlob.Download")`
2. Validate `key` and `destPath`
3. Resolve `dataPath` using `keyToPaths()`
4. Open source file and stat for size
5. Create destination parent directory
6. **Atomic write** (temp file + rename):
   - Generate temp path: `tmpDest := destPath + fmt.Sprintf(".tmp-%d-%x", ...)`
   - Create temp file
   - Copy data with `io.Copy()`
   - Fsync temp file and close
   - On Windows: `os.Remove(destPath)` if exists
   - Rename: `os.Rename(tmpDest, destPath)`
   - Fsync parent directory
7. **Optional:** Restore metadata (if metadata file exists):
   - Read metadata JSON
   - Set mod time: `os.Chtimes(destPath, ...)`
   - Set permissions: `os.Chmod(destPath, ...)`
8. Calculate metrics from bytes copied
9. Add trace attributes
10. Return `TransferInfo`

**Error handling:**
- Clean up temp files on error
- Wrap errors with context

### 7. Factory Integration

**store/blob.go:**

Update `NewBlobStore()`:
```go
case LocalFileStore:
    return NewLocalFileBlob(ctx, bucketURL)
```

### 8. Testing

**store/file_test.go (new file):**

Test cases:
- URL parsing (with `~`, absolute paths, localhost)
- Key validation (valid keys, invalid characters, traversal attempts)
- Path safety (containment checks, relative path attacks)
- Upload (basic, with metadata, overwrite existing)
- Download (basic, restore metadata, missing file)
- Error cases (invalid keys, disk full, permissions)
- Windows-specific behavior (rename semantics)
- Basic concurrency (last-writer-wins)

## Platform-Specific Considerations

### Windows
- **Rename behavior:** Not atomic when destination exists
- **Mitigation:** Remove destination before rename
- **Trade-off:** Last-writer-wins semantics (acceptable for cache use case)
- **Advanced option:** Use `syscall.ReplaceFile` or Windows `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING` for true atomic replace

### Unix/Linux
- `os.Rename()` is atomic even when destination exists
- Fsync parent directory after rename for durability

## Rationale and Design Decisions

### Direct Key-to-Path Mapping
- **Pro:** Simple, predictable, no additional mapping logic
- **Pro:** Easy to debug and inspect
- **Con:** Potential for large flat directories with many keys at same level
- **Mitigation:** Encourage hierarchical keys (e.g., `project/branch/artifact`)
- **Future:** Can add hash-based fan-out if needed

### Atomic Operations (Temp + Rename)
- **Pro:** Readers never see partial/corrupted files
- **Pro:** Standard POSIX pattern
- **Pro:** Works across platforms with minor adjustments
- **Con:** Requires cleanup on errors
- **Con:** Windows needs extra handling for overwrite

### JSON Sidecar Metadata
- **Pro:** Portable across all platforms
- **Pro:** Transparent and debuggable
- **Pro:** More reliable than extended attributes
- **Con:** Extra file per cache entry (acceptable overhead)
- **Con:** Metadata and data not updated atomically (write data first, then metadata)

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Path traversal attacks | High | Strict key validation + `filepath.Rel()` containment checks |
| Concurrent writes to same key | Medium | Last-writer-wins via atomic rename (acceptable for cache) |
| Symlink traversal | Medium | Document "no symlinks under root" contract; or check with `EvalSymlinks` |
| Disk full during write | Medium | Return clear error; ensure temp files cleaned up |
| Windows rename semantics | Low | Remove destination first; document last-writer-wins behavior |
| Large flat directories | Low | Encourage hierarchical keys; add sharding later if needed |

## Future Enhancements (Out of Scope)

### Locking for Concurrent Access
- Use per-key `.lock` files with `O_CREATE|O_EXCL`
- Implement backoff/retry with timeout
- Improves safety for high-contention scenarios

### Hashed Fan-Out Structure
- Compute `sha256(key)` to determine storage path
- Example: `root/objects/aa/bb/<escaped-key>`
- Maintains index mapping key → path
- Better for very large caches (tens of thousands of entries per directory)

### Context Cancellation Support
- Wrap reader in context-aware reader that aborts on `ctx.Done()`
- More complex but better for long-running transfers

### Checksum Verification
- Always compute SHA256 during upload
- Verify on download
- Detect corruption/tampering

## Implementation Effort

**Estimated effort:** 0.5–1 day

**Tasks:**
1. Implement `LocalFileBlob` with parsing and validation
2. Implement `Upload` with atomic write and metadata
3. Implement `Download` with atomic write
4. Wire into factory
5. Write comprehensive tests
6. Test on both Unix and Windows platforms

## Success Criteria

- [ ] Accepts `file://` URLs with `~` expansion
- [ ] Stores files and metadata in documented structure
- [ ] Upload and Download work correctly
- [ ] Atomic operations prevent partial reads
- [ ] Path traversal attempts are blocked
- [ ] Metrics match S3/NSC implementations
- [ ] Tests cover success and error cases
- [ ] Works on both Unix and Windows
