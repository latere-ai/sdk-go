# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

# Every gate lives in lateregate, pinned as a tool in internal/gate/go.mod, a
# module of its own so the SDK's go.mod stays free of requirements. A
# contributor needs nothing but Go. The plan: `go tool lateregate list`. One gate:
# `go tool lateregate <gate>`. The floor and the settings are in
# .lateregate.yaml.

LATEREGATE := go -C internal/gate tool lateregate
GATE_ROOT := -C ../..

.PHONY: check test race cover fmt fmt-check lint vuln wasm hooks release release-notes openapi

## check: the whole shared bar, then the browser build
check: wasm
	@$(LATEREGATE) $(GATE_ROOT)

## test: every test
test:
	@$(LATEREGATE) test $(GATE_ROOT)

## race: every test with the race detector
race:
	@$(LATEREGATE) race $(GATE_ROOT)

## cover: the per-package coverage floor
cover:
	go test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	@$(LATEREGATE) cover $(GATE_ROOT) -profile=../../coverage.out

## fmt: format every Go file
fmt:
	gofmt -w .

## fmt-check: fail when a Go file is not formatted
fmt-check:
	@$(LATEREGATE) fmt-check $(GATE_ROOT)

## lint: golangci-lint at the version lateregate pins
lint:
	@$(LATEREGATE) lint $(GATE_ROOT)

## vuln: govulncheck at the version lateregate pins, over every package
vuln:
	@$(LATEREGATE) vuln $(GATE_ROOT)

## wasm: the packages a browser runs build for WebAssembly
wasm:
	GOOS=js GOARCH=wasm go vet ./mls ./messaging ./attachment

## hooks: install the git hooks
hooks:
	git config core.hooksPath .githooks
	@echo "installed git hooks (core.hooksPath=.githooks)"

## release: cut VERSION from the notes under Unreleased in CHANGELOG.md
release:
	@$(LATEREGATE) release $(GATE_ROOT) "$(VERSION)"

## release-notes: print the changelog section for TAG, or fail
release-notes:
	@$(LATEREGATE) release-notes $(GATE_ROOT) "$(TAG)"

## openapi: refresh the API documents the tests hold the models to
openapi:
	cd tools && go run ./openapi -dir ../openapi
