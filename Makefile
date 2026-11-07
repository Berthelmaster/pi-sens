GO      ?= go
NPM     ?= npm
BIN     := bin
PKG     := ./cmd/pisens
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# The Go packages to vet, test and lint. web/node_modules sits inside the module
# and ./... walks into it, so an npm package that ships a .go file would break
# the run; leave those directories out. -e keeps such a broken package from
# failing the listing itself. The template turns each package directory into a
# ./path relative to the module root. CI reads the list with `make -s go-packages`.
GOPKGS = $(shell $(GO) list -e -f '.{{slice .Dir (len .Root)}}' ./... | grep -v -E '/node_modules(/|$$)')

.PHONY: build build-arm64 web web-check test vet lint go-packages shellcheck test-deploy run clean

## build: build the frontend and pisens for the host into bin/pisens
build: web
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pisens $(PKG)

## build-arm64: build the frontend and a static linux/arm64 binary (Raspberry Pi) into bin/pisens-linux-arm64
build-arm64: web
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/pisens-linux-arm64 $(PKG)

## web: install frontend dependencies and build web/dist, which the binary embeds
web: web/node_modules/.package-lock.json
	cd web && $(NPM) run build

# npm ci only reruns when the lockfile or package.json changes.
web/node_modules/.package-lock.json: web/package.json web/package-lock.json
	cd web && $(NPM) ci

## web-check: type-check the frontend, check its formatting and run its unit tests
web-check: web/node_modules/.package-lock.json
	cd web && $(NPM) run check

## test: run go vet and the unit tests
test: vet
	$(GO) test $(GOPKGS)

## vet: run go vet
vet:
	$(GO) vet $(GOPKGS)

## lint: run golangci-lint with .golangci.yml (needs golangci-lint v2 on PATH)
lint:
	golangci-lint run $(GOPKGS)

## go-packages: print the Go package directories that vet, test and lint cover
go-packages:
	@echo $(GOPKGS)

## shellcheck: check the deploy scripts and their tests with shellcheck (needs shellcheck on PATH)
shellcheck:
	shellcheck deploy/*.sh deploy/tests/*.sh deploy/tests/stubs/*

## test-deploy: test deploy/deploy.sh with stub systemctl and curl (no root needed)
test-deploy:
	deploy/tests/deploy_test.sh

## run: build and run pisens; pass flags with ARGS, e.g. make run ARGS='--listen :9090'
run: build
	./$(BIN)/pisens $(ARGS)

## clean: remove build output, keeping the web/dist placeholder
clean:
	rm -rf $(BIN)
	find web/dist -mindepth 1 ! -name .gitkeep -delete
