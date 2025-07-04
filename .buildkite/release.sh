#!/bin/env bash

#
# This script is used to build a release of the CLI and publish it to multiple registries on Buildkite
#

# NOTE: do not exit on non-zero returns codes
set -uo pipefail

export GORELEASER_KEY=""

if ! GORELEASER_KEY=$(buildkite-agent secret get goreleaser_key); then
    echo "Failed to retrieve GoReleaser Pro key"
    exit 1
fi

echo "--- :goreleaser: Building release with GoReleaser"

if ! goreleaser "$@"; then
    echo "Failed to build a release"
    exit 1
fi