# zstash

WIP of a cache save and restore tool.

# Usage

Save usage.

```
Usage: zstash save --local-cache-path=STRING <path> ... [flags]

save files.

Arguments:
  <path> ...    Paths to remove.

Flags:
  -h, --help                       Show context-sensitive help.
      --version
      --debug                      Enable debug mode.

      --key=STRING                 Key to save, this can be a template string.
      --local-cache-path=STRING    Local cache path ($LOCAL_CACHE_PATH).
      --remote-cache-url=STRING    Remote cache URL ($REMOTE_CACHE_URL).
      --expires-in-secs=86400      Expires in seconds.
      --encoder-concurrency=8      Encoder concurrency.
```

Restore usage.

```
Usage: zstash restore <path> ... [flags]

restore files.

Arguments:
  <path> ...    Paths within the cache archive to restore to the restore path.

Flags:
  -h, --help                       Show context-sensitive help.
      --version
      --debug                      Enable debug mode.

      --key=STRING                 Key to restore.
      --local-cache-path=STRING    Local cache path ($LOCAL_CACHE_PATH).
      --restore-path="."           Path to restore ($RESTORE_PATH).
      --remote-cache-url=STRING    Remote cache URL ($REMOTE_CACHE_URL).
```

## Key templates

When your saving or restoring a key you can pass a template for the key name.

Currently the template has the following inbuilt functions.

- `shasum` this will read the provided file path and build a sha256 checksum then insert that into key name.
- `env` this function takes a key and looks it up in the local environment and returns error if it doesn't exist.

> [!NOTE]
> When building a cache key missing environment variables are more important as we are aiming to be more explicit with the match of an archive.

# Examples

Save local node modules with only a local cache.

```
zstash save --local-cache-path /tmp --key '{{ env "BUILDKITE_PIPELINE_NAME" }}/{{ env "BUILDKITE_BRANCH" }}-{{ shasum "./package-lock.json" }}' ./node_modules/
```

Restore local node modules with only a local cache.

```
zstash restore --debug --key '{{ env "BUILDKITE_PIPELINE_NAME" }}/{{ env "BUILDKITE_BRANCH" }}-{{ shasum "./package-lock.json" }}' --local-cache-path /tmp node_modules
```

# Verification

To verify the cache and restore worked you can use diff.

```bash
diff --recursive ../vite-artifact-demo/app/node_modules node_modules
```