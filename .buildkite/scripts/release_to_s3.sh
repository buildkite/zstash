#!/bin/bash

set -euo pipefail

git pull

# This script is used to release the build artifacts to S3
goreleaser release --snapshot --clean

# Upload the build artifacts to S3
for i in `ls -1 dist/*.gz`
do
echo $i
aws s3 cp $i s3://${RELEASE_BUCKET_USA}/zstash/
done

for i in `ls -1 dist/*.gz`
do
echo $i
aws s3 cp $i s3://${RELEASE_BUCKET_EU}/zstash/
done