.PHONY: build docs release-check

GORELEASER ?= goreleaser
build:
	mkdir -p bin
	go build -o bin/terraform-provider-hue .
	go build -o bin/hue-tf ./cmd/hue-tf

docs:
	sh scripts/generate-docs.sh

# Build unsigned local archives without creating a tag or publishing a release.
release-check:
	$(GORELEASER) check --config .goreleaser.yml
	$(GORELEASER) check --config .goreleaser-cli.yml
	$(GORELEASER) release --snapshot --clean --skip=publish,sign --config .goreleaser.yml
	$(GORELEASER) release --snapshot --clean --skip=publish,sign --config .goreleaser-cli.yml
