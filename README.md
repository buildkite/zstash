# zstash

WIP of a cache save and restore tool which uses a WIP feature in the buildkite api.

# Usage

Save usage.

```
Usage: zstash save --token=STRING --id=STRING --key=STRING --organization=STRING --branch=STRING --pipeline=STRING [flags]

save files.

Flags:
  -h, --help                     Show context-sensitive help.
      --version
      --debug                    Enable debug mode ($BUILDKITE_ZSTASH_DEBUG).
      --registry-slug="~"        The registry slug to use
                                 ($BUILDKITE_REGISTRY_SLUG).
      --endpoint="https://agent.buildkite.com/v3"
                                 The endpoint to use. Defaults to the Buildkite
                                 agent API endpoint.
      --token=STRING             The buildkite agent access token to use
                                 ($BUILDKITE_AGENT_ACCESS_TOKEN).
      --trace-exporter="noop"    The trace exporter to use. Defaults to 'noop'
                                 ($BUILDKITE_ZSTASH_TRACE_EXPORTER).

      --id=STRING                ID of the cache entry to save.
      --key=STRING               Key of the cache entry to save, this can be a
                                 template string.
      --store="s3"               store used to upload / download
      --format="zip"             the format of the archive
      --paths=STRING             Paths to remove.
      --organization=STRING      The organization to use
                                 ($BUILDKITE_ORGANIZATION_SLUG).
      --branch=STRING            The branch to use ($BUILDKITE_BRANCH).
      --pipeline=STRING          The pipeline to use ($BUILDKITE_PIPELINE_SLUG).
      --bucket-url=STRING        The bucket URL to use
                                 ($BUILDKITE_CACHE_BUCKET_URL).
      --prefix=STRING            The prefix to use ($BUILDKITE_CACHE_PREFIX).
      --skip                     Skip saving the cache entry
                                 ($BUILDKITE_CACHE_SKIP).
```

Restore usage.

```
Usage: zstash restore --token=STRING --id=STRING --key=STRING [flags]

restore files.

Flags:
  -h, --help                     Show context-sensitive help.
      --version
      --debug                    Enable debug mode ($BUILDKITE_ZSTASH_DEBUG).
      --registry-slug="~"        The registry slug to use
                                 ($BUILDKITE_REGISTRY_SLUG).
      --endpoint="https://agent.buildkite.com/v3"
                                 The endpoint to use. Defaults to the Buildkite
                                 agent API endpoint.
      --token=STRING             The buildkite agent access token to use
                                 ($BUILDKITE_AGENT_ACCESS_TOKEN).
      --trace-exporter="noop"    The trace exporter to use. Defaults to 'noop'
                                 ($BUILDKITE_ZSTASH_TRACE_EXPORTER).

      --id=STRING                ID of the cache entry to restore.
      --key=STRING               Key of the cache entry to restore, this can be
                                 a template string.
      --store="s3"               store used to upload / download
      --format="zip"             the format of the archive
      --paths=STRING             Paths within the cache archive to restore to
                                 the restore path.
      --organization=STRING      The organization to use
                                 ($BUILDKITE_ORGANIZATION_SLUG).
      --branch=STRING            The branch to use ($BUILDKITE_BRANCH).
      --pipeline=STRING          The pipeline to use ($BUILDKITE_PIPELINE_SLUG).
      --bucket-url=STRING        The bucket URL to use
                                 ($BUILDKITE_CACHE_BUCKET_URL).
      --prefix=STRING            The prefix to use ($BUILDKITE_CACHE_PREFIX).
```

## Key templates

When your saving or restoring a key you can pass a template for the key name.

Currently the template has the following inbuilt functions.

- `id` this is the unique id of the cache, examples of this are `node`, `ruby`, or if you have more than one add another label such as `node-frontend` or `node-backend`.
- `checksum` this will read the provided file path and build a sha1 checksum then insert that into key name.
- `agent.os` this is the operating system of the agent.
- `agent.arch` this is the CPU architecture of the agent.

> [!NOTE]
> When building a cache key missing environment variables are more important as we are aiming to be more explicit with the match of an archive.

# Verification

To verify the cache and restore worked you can use diff.

```bash
diff --recursive ../vite-artifact-demo/app/node_modules node_modules
```

# Tracing

To enable tracing you need to export the following, to do this you can use [direnv](https://direnv.net/).

The following configuration enables grpc transport and sends the data to [honeycomb](https://www.honeycomb.io/distributed-tracing). Update the `API_TOKEN_HERE` value with the honeycomb api token.

```
export TRACE_EXPORTER=grpc
export OTEL_SERVICE_NAME=zstash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://api.honeycomb.io:443
export OTEL_EXPORTER_OTLP_HEADERS=x-honeycomb-team=API_TOKEN_HERE,x-honeycomb-dataset=dev
```