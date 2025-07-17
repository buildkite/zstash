# zstash

WIP of a cache save and restore tool.

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

### Supported Storage Backends

The gocloud.dev implementation supports multiple storage backends:

1. **AWS S3**: `s3://bucket-name?region=us-east-1`
2. **Google Cloud Storage**: `gs://bucket-name` (add `_ "gocloud.dev/blob/gcsblob"`)
3. **Azure Blob Storage**: `azblob://bucket-name` (add `_ "gocloud.dev/blob/azureblob"`)
4. **Local File System**: `file:///path/to/directory` (for testing)

### Adding New Cloud Providers

To add support for a new cloud provider:

1. Import the appropriate gocloud.dev driver:
   ```go
   import _ "gocloud.dev/blob/gcsblob" // For Google Cloud Storage
   ```

2. Use the provider-specific URL format when creating the blob storage.

## 📝 License

MIT © Buildkite

SPDX-License-Identifier: MIT