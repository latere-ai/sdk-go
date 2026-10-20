# Working in this repository

Notes for coding agents and other automated contributors. People start
with [CONTRIBUTING.md](CONTRIBUTING.md); everything here applies to them
too.

## Layout

- `latere/`: the core every package shares (configuration, credentials,
  errors, retries, pagination, streams). Capability packages call the API
  only through it.
- `agents/`, `account/`, `apps/`: one package per capability of the API, named for
  the capability.
- `signin/`: signing a person in. The core must not import it.
- `internal/sse/`: the server-sent events reader.
- `openapi/`: the API's documents the spec tests read. Refresh them with
  `make openapi`; never edit them by hand.
- `tools/`: a separate module for the refresh program.
- `docs/design.md`: why the code is shaped as it is. Keep it true when a
  change alters a decision it describes.

## The gate

`make check` is the one bar: gofmt, go vet, staticcheck, govulncheck and
`go test -race` over every package. Run it before every commit; CI runs the
same targets.

## Rules

- The standard library is the only dependency of the main module. The
  check in CI fails when `go.mod` gains a `require`.
- Every Go file starts with the Apache-2.0 header the existing files carry.
- Every exported name has a doc comment for a reader who sees that one
  name: what it does, what it returns, when it errors.
- Follow the design rules in [CONTRIBUTING.md](CONTRIBUTING.md): exported
  fields only on plain data and configuration filled in once; one method
  per endpoint on a capability's client, named verb first; named types for
  statuses, states and kinds; `time.Time` for timestamps.
- Comments in code say what the code does and why, never the history of a
  change.
- Handle every error; a dropped one carries a comment saying why that is
  safe.
- Tests run against `httptest` servers, never the live API, and a change
  comes with a test that fails without it. Keep coverage where it is.
- A model field added or left out must keep the package's `spec_test.go`
  passing; an omission is listed there with its reason.
- Name things with the API's public vocabulary from
  [platform.latere.ai/docs](https://platform.latere.ai/docs).
- American spelling. No em dashes in prose or comments.
- Add a line to `CHANGELOG.md` under `Unreleased` for any change a user
  would notice.
- Commit messages read `scope: lowercase description`, where the scope is
  a package or area (`agents`, `latere`, `docs`, `ci`).
