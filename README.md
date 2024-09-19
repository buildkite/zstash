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

      --key=STRING                 Key to save.
      --local-cache-path=STRING    Local cache path ($LOCAL_CACHE_PATH).
      --remote-cache-url=STRING    Remote cache URL ($REMOTE_CACHE_URL).
      --expires-in-secs=86400      Expires in seconds.
      --multi-threaded             Enable multi-threaded compression.
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


# Examples

Save local node modules with only a local cache.

```
zstash save --debug --key blah-123 --local-cache-path /tmp ./node_modules
```

Restore local node modules with only a local cache.

```
zstash restore --debug --key blah-123 --local-cache-path /tmp node_modules
```