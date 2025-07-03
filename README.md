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

## 📝 License

MIT © Buildkite

SPDX-License-Identifier: MIT