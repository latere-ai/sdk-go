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
- Package `agents`: create, list, read, fork, archive, unarchive, end and
  delete sessions; read a session's events page by page or follow them
  live; send messages, interrupt, answer questions, approve or deny tool
  calls, and send the results of tools your program runs. Typed payloads
  for the agent's responses, tool calls and their results, status changes
  and session errors.
- Package `account`: Effort levels, the wallet, preferences, pinning
  sessions, the person's computers, and applying a managed agent.
- Package `signin`: signing a person in through their browser or with a
  device code, refreshing the sign-in, reading who signed in, and signing
  out, for programs whose client Latere registers.
- Package `llm`: calling models in the Lux dialect. Generate an answer,
  stream it as typed events, count a request's tokens, and list the models
  a key may call; label requests for your own reporting. It replaces the
  `luxsdk` package of `latere.ai/x/pkg`, with the same wire types; calls
  straight to a provider with your own key, which `luxsdk` also made, are
  `latere.ai/x/pkg/llmdirect`, on these types.
- `latere.Request.SendKey` sends the key itself, which the models
  capability authenticates, and an error names its request from the error's
  details when the response carries no `X-Request-Id`.
