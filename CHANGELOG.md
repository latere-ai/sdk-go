# Changelog

All notable changes to this SDK are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the SDK
follows [semantic versioning](https://semver.org/); while it is at v0, a
minor version may break code, and every such change is marked **Breaking**.

## [Unreleased]

### Added

- The core package `latere`: one configuration shared by every package,
  credentials from a key (exchanged for short-lived tokens and renewed) or
  from a person's sign-in, typed errors that work with `errors.Is` and
  `errors.As`, retries with backoff and jitter for requests that are safe
  to repeat, a default timeout of 80 seconds per call, pagination as Go
  iterators, and resumable event streams.
- Package `agents`: create, list, read, change, fork, archive, unarchive,
  resume, end and delete sessions; read a session's events page by page or
  follow them live; send messages, interrupt, answer questions, approve or
  deny tool calls and connections, and send the results of tools your
  program runs. Typed payloads for the agent's responses, tool calls with
  the approval policy's verdict, their results, connection approvals,
  status changes and session errors.
- Package `account`: Effort levels, the wallet, preferences, pinning
  sessions, the person's computers, and applying a managed agent.
- Package `apps`: create, list, read, change and delete apps; list and
  read their deploys; read a deploy's build log or follow it live, resuming
  after a dropped connection; list and read releases; and read, invite to,
  revoke and password-protect who may open a private app. Every model
  keeps the JSON the API answered, returned by `RawJSON`.
- Package `agents`: triggers, which start sessions on a schedule or when
  an event arrives: apply, list, read, fire and delete them, and list their
  firings.
- Package `connections`: the services a person connects once and agents
  use without holding the credential: list connectors, add, change and
  remove MCP servers of one's own, connect a service with a key or a
  sign-in, finish and renew a sign-in, replace a key, rename and
  disconnect connections, and list a connection's tools and calls.
- Package `signin`: signing a person in through their browser or with a
  device code, refreshing the sign-in, reading who signed in, and signing
  out, for programs whose client Latere registers.
