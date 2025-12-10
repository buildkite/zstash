# zstash

Go cache library providing cache save/restore and API functionality for the [Buildkite Agent](https://github.com/buildkite/agent).

# Cache Key Pattern Matching

zstash supports full glob pattern matching for cache keys using the [zzglob](https://pkg.go.dev/drjosh.dev/zzglob) library.

# S3 Self-Managed Bucket

When using S3 as the storage backend (`local_s3` store type), configure the bucket URL with query parameters to customize behavior.

## URL Format

```
s3://bucket-name[/prefix][?options]
```

## Options

| Parameter | Description | Default | Valid Range |
|-----------|-------------|---------|-------------|
| `region` | AWS region for the bucket | `us-east-1` | Any valid AWS region |
| `endpoint` | Custom S3 endpoint (for S3-compatible storage or local testing) | AWS default | Any valid URL |
| `use_path_style` | Use path-style addressing instead of virtual-hosted-style | `false` | `true` or `false` |
| `concurrency` | Number of parallel upload/download parts | `5` | 0-100 (0 = default) |
| `part_size_mb` | Size of each part in MB for multipart transfers | `5` | 0, or 5-5120 (0 = default) |

## Examples

Basic S3 bucket:
```
s3://my-cache-bucket
```

S3 bucket with prefix and region:
```
s3://my-cache-bucket/buildkite/cache?region=us-west-2
```

S3-compatible storage (e.g., MinIO) with path-style access:
```
s3://my-bucket?endpoint=http://localhost:9000&use_path_style=true
```

High-performance configuration for large files:
```
s3://my-cache-bucket?concurrency=20&part_size_mb=100
```

All options combined:
```
s3://my-cache-bucket/prefix?region=eu-west-1&concurrency=10&part_size_mb=50
```

## Notes

- **Part size**: AWS S3 requires a minimum part size of 5 MB and maximum of 5 GB (5120 MB) for multipart uploads.
- **Concurrency**: Higher concurrency can improve throughput for large files but uses more memory and network connections.
- **Endpoint**: Use for S3-compatible storage like MinIO, LocalStack, or custom endpoints.

# API Documentation

See the [API documentation on pkg.go.dev](https://pkg.go.dev/github.com/buildkite/zstash) for details.

## License

MIT © Buildkite

SPDX-License-Identifier: MIT