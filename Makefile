APPNAME=buildkite-cache
STAGE=dev

.PHONY: cache-bucket
cache-bucket:
	@echo "--- deploy stack $(APPNAME)-$(STAGE)-cache-bucket"
	@sam deploy \
		--no-fail-on-empty-changeset \
		--template-file sam/app/cache-bucket.cfn.yml \
		--capabilities CAPABILITY_IAM \
		--tags "environment=$(STAGE)" "application=$(APPNAME)" \
		--stack-name $(APPNAME)-$(STAGE)-cache-bucket \
		--parameter-overrides AppName=$(APPNAME) Stage=$(STAGE) CachePrefix=cache