# Changelog

All notable changes to this SDK are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the SDK
follows [semantic versioning](https://semver.org/); while it is at v0, a
minor version may break code, and every such change is marked **Breaking**.

## [Unreleased]

### Added

- Package `storage`: write, read, inspect and list the files of Latere
  Storage, answer a download link, and upload a file of any size up to
  5 GiB, in one request or in parts put straight to object storage, with
  its progress and the write's conditions. The steps of an upload in
  parts are there on their own too; an upload that fails once it is open
  is given up and named by an `*UploadError`.
- Package `agents`: a message carries pictures, built with `ImageBlock`,
  and files, `SendMessageParams.Attachments`, by their bytes or by their
  path in Storage, and may hold files alone. A `UserMessage` records the
  files it carried. The limits the platform holds a message to are
  constants, such as `MaxAttachmentBytes` and `MaxSourcePath`.
- The core `latere`: `Request.Content` sends raw bytes of a declared
  length, read again from the start for a retry; `Request.SignedURL` sends
  a request to a signed link without the credential; `Request.KeepRedirect`
  has `Config.Open` return a redirect rather than follow it;
  `RetryAnswers` sends a request again after an answer that says it
  failed but never after a failure on the network; and
  `ErrPreconditionFailed` matches a 412. A request with `Content` bounds
  each attempt by the timeout and `TransferTime` for its length and sends
  an attempt cut off again. An error never names a signed URL's query.

## [0.1.0] - 2026-10-20

The first release.

### Added

- The core package `latere`: one configuration shared by every package,
  credentials from a key (exchanged for short-lived tokens and renewed) or
  from a person's sign-in, typed errors that work with `errors.Is` and
  `errors.As`, retries with backoff and jitter for requests that are safe
  to repeat, a default timeout of 80 seconds per call, pagination as Go
  iterators, and resumable event streams. A call that cannot get
  credentials returns a `*latere.TokenError` naming the cause, and an
  `*latere.Error` keeps the body the API sent in `Body` and names its
  request from the error's details when the response carries no
  `X-Request-Id`. `Request.SendKey` sends the key itself, which the models
  capability authenticates.
- Package `agents`: create, list, read, change, fork, archive, unarchive,
  resume, end and delete sessions; read a session's events page by page or
  follow them live; send messages, interrupt, answer questions, approve or
  deny tool calls and connections, and send the results of tools your
  program runs. Typed payloads for the agent's responses, tool calls with
  the approval policy's verdict, their results, connection approvals,
  status changes and session errors. Triggers, which start sessions on a
  schedule or when an event arrives: apply, list, read, fire and delete
  them, and list their firings; a firing that failed to start its session
  is returned along with its error.
- Package `account`: Effort levels, the wallet, preferences, pinning
  sessions, the person's computers, and applying a managed agent.
- Package `apps`: create, list, read, change and delete apps; list and
  read their deploys; read a deploy's build log or follow it live, resuming
  after a dropped connection; list and read releases; and read, invite to,
  revoke and password-protect who may open a private app.
- Package `connections`: the services a person connects once and agents
  use without holding the credential: list connectors, add, change and
  remove MCP servers of one's own, connect a service with a key or a
  sign-in, finish and renew a sign-in, replace a key, rename and
  disconnect connections, and list a connection's tools and calls.
- Package `llm`: calling models in the Lux dialect. Generate an answer,
  stream it as typed events, count a request's tokens, and list the models
  a key may call; label requests for your own reporting. It replaces the
  `luxsdk` package of `latere.ai/x/pkg`, with the same wire types; calls
  straight to a provider with your own key, which `luxsdk` also made, are
  `latere.ai/x/pkg/llmdirect`, on these types.
- Package `llm/wire`: the Lux dialect's types alone, importing the standard
  library and nothing that sends a request, for code that reads or writes
  the format without calling the API. `llm` names all of them.
- Package `signin`: signing a person in through their browser or with a
  device code, refreshing the sign-in, reading who signed in, and signing
  out, for programs whose client Latere registers.
- Every resource the `agents`, `account`, `apps` and `connections`
  packages return keeps the JSON the API sent, returned by `RawJSON`, with
  the fields the package does not model.

### Changed

For programs built on a commit before this release, such as
`v0.0.0-20261020132337-a96fb9a47778`. The JSON each type reads and writes
is unchanged unless an entry says otherwise.

- **Breaking:** Names say their unit and their kind.
  `agents.ResumeSessionParams.MaxCostUSDMicro` is `MaxCostMicroUSD`, and
  `wire.Usage.CostUSDMicro` is `CostMicroUSD`. `agents.Allow` and
  `agents.Deny` are `DecisionAllow` and `DecisionDeny`. `agents.PolicyNew`
  and `agents.PolicyContinue` are `SessionPolicyNew` and
  `SessionPolicyContinue`.
- **Breaking:** Effort names only the tier a model name chooses; a model's
  depth of thought is its reasoning level. In `llm` and `llm/wire`,
  `Effort` is `ReasoningLevel`, `EffortMinimal` to `EffortHigh` are
  `ReasoningMinimal` to `ReasoningHigh`, and `Reasoning.Effort` is
  `Reasoning.Level`. `agents.Model.Reasoning` is an
  `agents.ReasoningLevel`, with the same constants.
- **Breaking:** `agents.UpdateSessionParams.Model` is an
  `*agents.ModelChange`, which changes the model, its reasoning level or
  both; `AgentReasoning` returns to the level the agent sets. Before, the
  reasoning level could not change alone or go back to the agent's.
  `Metadata` sets the labels it names and keeps the others, as the API
  always did, and `DeleteMetadata` removes labels.
- **Breaking:** Values a program branches on have named string types with
  constants: `agents.ToolResult.Outcome` (`ToolOutcome`),
  `agents.ApprovalRequested.Source` (`ApprovalSource`),
  `agents.SessionResource.Access` (`Access`), `apps.Release.Via`
  (`ReleaseVia`), `apps.Deploy.Client` (`DeployClient`), `apps.LogLine.Step`
  (`BuildStep`), `apps.SkippedInvite.Reason` (`SkipReason`) and
  `connections.Connection.Reason` (`Reason`). A comparison with a quoted
  string still compiles; a string variable needs a conversion.
- **Breaking:** `latere.Request.Retry` takes a `latere.RetryRule`. By
  default an idempotency key makes only a `POST` safe to repeat, since the
  API reads the key on `POST` alone; before, a key made a request of any
  method retried. `RetryAlways` and `RetryNever` set the rule whatever the
  method.
- A `PATCH` that only sets values is retried: `agents.UpdateSession`,
  `apps.UpdateApp`, `connections.RenameConnection` and
  `connections.UpdateConnector`. `agents.UpdateSession` and
  `llm.CountTokens`, also retried, no longer send a key the API does not
  read.
- `agents.FireTrigger` is sent once, never again after a failure: the API
  forgets a firing's key when it answers with a server error, so a repeat
  would fire the trigger again. Pass the same `IdempotencyKey` to fire
  again yourself. `apps.CreateApp`, `apps.Invite`,
  `connections.ConfirmConnection`, `connections.ReconnectConnection`,
  `llm.Generate` and `llm.Stream` were already sent once, and their docs
  now say why.
- **Breaking:** An error answer without the API's envelope, such as a
  proxy's page, has the HTTP status text as `Error.Message`; before, its
  first 200 bytes. `Error.Body` holds the bytes, cut at a character
  boundary. An envelope cut at 64 KiB still gives its code and message.
- **Breaking:** `connections.UpdateConnector` refuses a change that
  changes nothing before sending it.
- `llm.WithLabels` leaves out a pair the service would drop: past the
  eighth, or a key or value with a character outside the service's rule,
  such as `,` or `=`, which could otherwise change another pair.
- `agents.TriggerEvent.Subject` names the person an event is about.
- `account.Pins`, `account.Devices` and `connections.Connected` keep the
  JSON the API sent behind `RawJSON`, as every other resource does.
- The `connections` examples return to a page Latere accepts, the
  console's Connections page, and say that `ReturnTo` must be one.

### Fixed

- `agents.SessionResource.Access` is `readWrite` or `readOnly`, as the API
  sends it; the doc named `read_only`.
