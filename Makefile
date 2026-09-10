.PHONY: build docs
build:
	mkdir -p bin
	go build -o bin/terraform-provider-hue .
	go build -o bin/hue-tf ./cmd/hue-tf

docs:
	sh scripts/generate-docs.sh
