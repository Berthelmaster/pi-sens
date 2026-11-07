GO      ?= go
BIN     := bin
PKG     := ./cmd/pisens
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-arm64 test vet run clean

## build: build pisens for the host into bin/pisens
build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pisens $(PKG)

## build-arm64: build a static linux/arm64 binary (Raspberry Pi) into bin/pisens-linux-arm64
build-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pisens-linux-arm64 $(PKG)

## test: run go vet and the unit tests
test: vet
	$(GO) test ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## run: build and run pisens; pass flags with ARGS, e.g. make run ARGS='--listen :9090'
run: build
	./$(BIN)/pisens $(ARGS)

## clean: remove build output
clean:
	rm -rf $(BIN)
