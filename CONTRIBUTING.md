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

You need Go 1.26 or newer. On Linux and macOS, everything runs from the
repository root with `make`:

```sh
make test      # go test over every package
make check     # the shared gate, then the WebAssembly build
make cover     # the per-package coverage floor
make hooks     # install the pre-commit and pre-push hooks
```

The checks come from [lateregate](https://github.com/latere-ai/ci-gate), which
`internal/gate/go.mod` pins as a tool. The pin lives in a module of its own,
so the SDK's `go.mod` has no requirements and asks callers for no newer Go
than the SDK itself needs; the gate's own Go version applies to that module
alone, and it names the toolchain the checks run on. `make check` needs
nothing but Go. The settings, including the 90 percent coverage floor every
package clears on its own, are in `.lateregate.yaml`; `go -C internal/gate
tool lateregate list -C ../..` prints each check and whether it applies.
On Windows, run `go test -race ./...` directly.

CI runs the same gate on every pull request. Beside it, the tests run on
Linux, macOS and Windows, each on Go 1.26 and 1.27, and three checks run on
Linux: the packages a browser runs build for WebAssembly, `go.mod` requires
no module, and the refresh tool under `tools/` builds.

## How the code is laid out

- `latere/` is the core every package shares: configuration, credentials,
  the error type, retries, pagination and streams.
- `agents/`, `account/`, `apps/`, `connections/`, `evals/`, `llm/`, `memory/`, `storage/`, `web/` and later
  packages each cover one capability of the API, and call the API only
  through the core.
- `llm/wire/` holds the Lux dialect's request, response and stream types,
  which the service's codec imports too; it depends on the standard
  library alone.
- `signin/` signs a person in; the core does not depend on it.
- `internal/` holds code no caller imports.
- `openapi/` holds the API's OpenAPI documents the tests check the models
  against, and `tools/` the program that refreshes them.

[docs/design.md](docs/design.md) explains the reasons behind the layout,
credentials, retries, pagination and streams. Read it before proposing a
change to any of them.

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

## Design rules

[docs/design.md](docs/design.md) holds the reasons; these are the rules a
review checks:

- A type has exported fields only when it is plain data, such as a model
  or a request's parameters, or configuration filled in once before use.
  A type with state, invariants or concurrent use, such as a client or a
  token source, keeps its fields unexported behind a constructor and
  methods.
- A capability's client has one method per endpoint, named verb first
  after the API's resource: `GetSession`, `ListSessions`, `CreateSession`.
- A status, a state or a kind, a value a program branches on, has a
  named string type with constants. A timestamp is a `time.Time`.
- Every resource a capability returns keeps the JSON it was decoded from,
  behind `RawJSON`; the parts of a resource do not. `llm`'s types, the Lux
  dialect's own definition, are the exception.
- A method that creates or sends something by `POST` sends an
  idempotency key, generated when the caller gives none. A `PATCH` that
  only sets values sets `RetryAlways`. A call that a repeat could perform
  twice, because its part of the API takes no key, forgets the key on a
  failure, or a model may have run already, sets `RetryNever`; its doc
  says it is sent once, and the README and `latere/doc.go` list it.

## Pull requests

Keep a pull request to one change, with a title that says what it does.
CI must pass. A maintainer reviews it and may ask for changes; once it is
approved, a maintainer merges it.

## Releasing

A maintainer releases from `main`, with the changes for the release written
under `Unreleased` in [CHANGELOG.md](CHANGELOG.md), and runs one command:

```sh
go -C internal/gate tool lateregate -C ../.. release vX.Y.Z
```

It refuses while CI is red or `HEAD` is not the tip of `origin/main`, runs
the whole gate, moves the `Unreleased` notes to a `## vX.Y.Z - YYYY-MM-DD`
section, sets `latere.Version` to `X.Y.Z` in the same commit, tags it and
pushes the commit and the tag together. A test fails while `Version`
disagrees with the newest section of the changelog.

Pushing the tag publishes the GitHub release, with the tag's section as its
body, once CI has passed on the tagged commit; a tag on a commit whose CI
failed, or without a section, publishes nothing.

## License

By contributing, you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
