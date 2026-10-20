# Contributing

Thank you for helping. This page says how to build and test the SDK and how
a change gets in.

## Before you start

For a bug, open an issue with what you ran, what you expected and what
happened, including the request id from the error if there was one. For a
new feature or a change to the API's shape, open an issue first so we can
agree on the design before you write it. Small fixes, to docs or tests for
instance, can go straight to a pull request.

## Build and test

You need Go 1.26 or newer. Everything runs from the repository root:

```sh
make test      # go test -race over every package
make check     # gofmt, go vet, staticcheck, govulncheck and the tests
make cover     # the tests with a coverage report in coverage.html
```

`make check` is what CI runs on Linux, macOS and Windows, on both supported
Go versions. It needs nothing but Go: the linters run at pinned versions
through `go run`, and the module has no dependencies to install.

## How the code is laid out

- `latere/` is the core every package shares: configuration, credentials,
  the error type, retries, pagination and streams.
- `agents/`, `account/` and later packages each cover one capability of the
  API, and call the API only through the core.
- `signin/` signs a person in; the core does not depend on it.
- `internal/` holds code no caller imports.
- `openapi/` holds the API's OpenAPI documents the tests check the models
  against, and `tools/` the program that refreshes them.

## The models and the API's documents

The request and response types are written by hand from the API's OpenAPI
documents. Tests hold the two together: each field a model reads must be in
the document, each field the document requires must be in the model, and
each endpoint a package calls must exist. When the API changes, refresh the
documents and let the tests show what to change:

```sh
make openapi   # fetches the documents from platform.latere.ai/docs
make test
```

A field the document has and a model leaves out on purpose is listed, with
its reason, in that package's `spec_test.go`.

## Writing a change

- Add a test that fails without your change. Tests use `httptest` servers,
  never the live API.
- Every exported name has a doc comment that says what it does, what it
  returns, and when it errors. Add an `Example` function for a new method
  a caller would use directly.
- Handle every error. A dropped error needs a comment saying why it is
  safe to drop.
- Keep the standard library as the only dependency. A new dependency needs
  an issue that explains why first.
- Write for the reader: package docs and the README for a Go developer who
  has not used Latere before, comments in code for the next maintainer.
- Add a line to the `Unreleased` section of [CHANGELOG.md](CHANGELOG.md)
  saying what changes for someone who upgrades.

## Pull requests

Keep a pull request to one change, with a title that says what it does.
CI must pass. A maintainer reviews it and may ask for changes; once it is
approved, a maintainer merges it.

By contributing, you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
