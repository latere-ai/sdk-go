# Copyright 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

# The linters run at pinned versions through `go run`, so a contributor
# needs nothing but Go, and the module itself stays free of dependencies.
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: check fmt fmt-check vet staticcheck vulncheck test wasm cover openapi

## check: everything CI runs
check: fmt-check vet staticcheck vulncheck test wasm

## fmt: format every Go file
fmt:
	gofmt -w .

## fmt-check: fail when a Go file is not formatted
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

staticcheck:
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

## test: every test, with the race detector
test:
	go test -race -count=1 ./...

## wasm: the packages a browser runs build for WebAssembly
wasm:
	GOOS=js GOARCH=wasm go vet ./mls ./messaging

## cover: the tests with a coverage report in coverage.html
cover:
	go test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

## openapi: refresh the API documents the tests hold the models to
openapi:
	cd tools && go run ./openapi -dir ../openapi
