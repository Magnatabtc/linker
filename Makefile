GO ?= go
BINARY ?= linker

.PHONY: test build

test:
	$(GO) test ./...

build:
	$(GO) build -o $(BINARY) ./cmd/linker
