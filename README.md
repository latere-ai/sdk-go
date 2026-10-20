# Latere Go SDK

[![Go Reference](https://pkg.go.dev/badge/latere.ai/x/sdk-go.svg)](https://pkg.go.dev/latere.ai/x/sdk-go)
[![CI](https://github.com/latere-ai/sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/latere-ai/sdk-go/actions/workflows/ci.yml)

The official Go client for the [Latere API](https://platform.latere.ai/docs):
start sessions with agents and follow them as they work, connect the
services they use, publish apps, call models, and read your account. It
depends on the Go standard library alone.

```sh
go get latere.ai/x/sdk-go
```

The SDK has one package per capability of the API, plus a small core that
they share. Import the ones you use:

| Package | What it does |
|---|---|
| [`latere`](https://pkg.go.dev/latere.ai/x/sdk-go/latere) | The core: configuration, credentials, errors, retries, pagination |
| [`agents`](https://pkg.go.dev/latere.ai/x/sdk-go/agents) | Sessions with agents: create, list, follow, answer, approve, end; triggers that start them |
| [`connections`](https://pkg.go.dev/latere.ai/x/sdk-go/connections) | The services a person connects once, such as a calendar or an MCP server, which agents then use |
| [`account`](https://pkg.go.dev/latere.ai/x/sdk-go/account) | Your Effort levels, wallet, preferences, pins and computers |
| [`apps`](https://pkg.go.dev/latere.ai/x/sdk-go/apps) | Apps: create, list, change, delete; their deploys, build logs, releases and who may open them |
| [`llm`](https://pkg.go.dev/latere.ai/x/sdk-go/llm) | Calling models in the Lux dialect: generate, stream, count tokens, list models |
| [`llm/wire`](https://pkg.go.dev/latere.ai/x/sdk-go/llm/wire) | The Lux dialect's types alone, for code that reads or writes the format without calling the API |
| [`signin`](https://pkg.go.dev/latere.ai/x/sdk-go/signin) | Signing a person in, for apps Latere registers |

Create a key under **Keys** in the
[console](https://platform.latere.ai/console) and put it in the
`LATERE_KEY` environment variable. Then build one `latere.Config` and hand
it to each package's `New`:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

func main() {
	ctx := context.Background()
	cfg, err := latere.NewConfig() // reads LATERE_KEY; latere.ErrNoCredentials when it is unset
	if err != nil {
		log.Fatal(err)
	}

	w, err := account.New(cfg).GetWallet(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if w.SpendableMicroUSD != nil { // nil for a member of an organization
		fmt.Printf("%.2f USD left\n", float64(*w.SpendableMicroUSD)/1e6)
	}

	for s, err := range agents.New(cfg).ListSessions(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.ID, s.Status, s.Title)
	}
}
```

More packages will follow the same pattern: storage, repos and
environments.

## Requirements

Go 1.26 or 1.27. The SDK supports the two newest Go releases, as Go itself
does.

## Authentication

Every request carries a short-lived bearer token. Choose where it comes
from:

- **A key.** Create one under **Keys** in the console, choosing what it may
  do, and give it with `latere.WithKey` or in `LATERE_KEY`. The SDK
  exchanges the key for a token of about 15 minutes and renews it before
  it expires. Use a key for scripts, servers and CI.
- **A person's sign-in.** An app that Latere registers can sign a person in
  with package `signin` and call the API as them; see
  [Signing a person in](#signing-a-person-in).
- **Your own.** Implement `latere.TokenSource`, or wrap a token you already
  hold with `latere.StaticToken`.

A request acts in the context its token names: a key's context, or the
organization a person chose when signing in.

## Agents

A session is a conversation with an agent. Every message, answer, tool call
and state change is an event in the session's log.

```go
ag := agents.New(cfg)

s, err := ag.CreateSession(ctx, &agents.CreateSessionParams{
	Agent:   "release-notes",
	Message: "Write the release notes for v1.4.0.",
})
if err != nil {
	return err
}

_, err = ag.SendMessage(ctx, s.ID, &agents.SendMessageParams{
	Content: []agents.ContentBlock{agents.Text("Add a section for the breaking changes.")},
})
if err != nil {
	return err
}
```

A session that stops to ask a question, to have a tool call approved, or
to wait for a tool your program runs, is idle with `StopReason`
`agents.StopQuestion`, `agents.StopToolConfirmation` or
`agents.StopToolResult`. Answer with `AnswerQuestion`, `ConfirmTool` or
`SendToolResult`. End a session whose work is done with `EndSession`, or
remove it for good with `DeleteSession`.

Change a session's model, reasoning, approval mode, title or labels with
`UpdateSession`; an `agents.ModelChange` changes the model, its reasoning
level, or both, and `AgentReasoning` returns to the level the agent sets.
Resume a session that stopped on its budget with `ResumeSession`.

A trigger starts sessions on its own, on a schedule or when an event
arrives. Apply one by name; applying it again replaces it:

```go
_, err = ag.ApplyTrigger(ctx, &agents.Trigger{
	Metadata: agents.TriggerMetadata{Name: "weekly-digest"},
	Spec: agents.TriggerSpec{
		Agent:    "release-notes",
		Schedule: "0 9 * * 1",
		TimeZone: "Europe/Berlin",
		Session:  agents.TriggerSession{Message: "Summarize last week's releases."},
	},
})
```

The package covers sessions, their events and triggers. Defining agents,
searching sessions and a session's files are not covered yet; call them
with `Config.Do` (see [Configuration](#configuration)).

## Account

```go
acct := account.New(cfg)
levels, err := acct.GetEffortLevels(ctx) // what each Effort level costs now
devices, err := acct.ListDevices(ctx)    // the person's computers sessions can work on
_, err = acct.PinSession(ctx, s.ID)
```

## Apps

An app is a git repository and an address. A push to a branch builds a
preview, and a pushed tag that starts with `v` releases that commit to the
app's address; each build is a deploy with a log.

```go
ap := apps.New(cfg)
a, err := ap.CreateApp(ctx, &apps.CreateAppParams{Name: "Tide tables"})
if err != nil {
	return err
}
fmt.Println("push to", a.Repository.PushURL, "to publish at", a.URL)

deploys, err := ap.ListDeploys(ctx, a.Slug)
if err != nil {
	return err
}
if len(deploys) == 0 {
	return nil // nothing was pushed yet
}
for ev, err := range ap.FollowDeployLog(ctx, a.Slug, deploys[0].ID, nil) {
	if err != nil {
		return err
	}
	if ev.Line != nil {
		fmt.Println(ev.Line.Component, ev.Line.Text)
	}
}
```

Deploying is a git push, which the package does not do. Calls about who
may open a private app take the app's ID, which never changes; the others
take its slug.

## Connections

A person connects a service once, and agents use it without ever holding
the credential. A service that signs people in answers a page to send them
to, and the browser comes back to the `ReturnTo` page:

```go
cs := connections.New(cfg)
r, err := cs.CreateConnection(ctx, &connections.CreateConnectionParams{
	Connector: "google-calendar",
	ReturnTo:  "https://platform.latere.ai/console/connections",
})
if err != nil {
	return err
}
if r.AuthorizationURL != "" {
	fmt.Println("sign in at", r.AuthorizationURL)
}
```

`ReturnTo` must be a page Latere accepts: the console's Connections page,
which finishes the sign-in itself, or Latere Chat's connector settings. A
page of your own is refused with 400 until Latere registers apps' return
pages; an app it registers finishes the sign-in with `ConfirmConnection`
and the code its page receives.

`ListConnectors` lists the services a person may connect, and
`CreateConnector` adds an MCP server of their own.

## Models

Package `llm` calls models in the Lux dialect, one request, response and
streaming shape for every catalog model. The models capability takes the
key itself rather than a token exchanged for it, which the package handles:
build its Client from a `Config` whose credentials are a key.

```go
models := llm.New(cfg)
res, err := models.Generate(ctx, &llm.Request{
	Model:    "anthropic/claude-sonnet-4-5",
	Messages: []llm.Message{llm.UserText("Hello")},
})
if err != nil {
	return err
}
fmt.Println(res.Text())
```

`Stream` delivers the answer as typed events, `CountTokens` counts a
request's input tokens without spending any, and `ListModels` lists what
the key may call. A field the model's provider cannot represent is named in
`Result.Loss` rather than dropped silently. A generation is never sent a
second time after a failure, since the model may have run.

Programs built on the OpenAI or Anthropic SDKs reach the same models with
the same key through the models capability's compatible endpoints; see
[Providers and doors](https://platform.latere.ai/docs/models/providers).

## The API's own answer

Every resource the `agents`, `account`, `apps` and `connections` packages
return keeps the JSON the API sent. `RawJSON` returns it with every field,
including fields the API added after your version of the SDK, for a program
that shows or passes on the answer as it came, such as a command line's
`--json` output:

```go
s, err := ag.GetSession(ctx, id)
if err != nil {
	return err
}
os.Stdout.Write(s.RawJSON())
```

A resource your program builds itself has no JSON, and `RawJSON` returns
nil. The `llm` types need none: they are the Lux dialect's own definition.

## Streaming

`StreamEvents` follows a session's log as it grows, and ends after the
event that ends the session. When the connection drops it reconnects and
resumes after the last event it delivered, so you see each event exactly
once. Ask for deltas to show a response while the agent writes it:

```go
for item, err := range ag.StreamEvents(ctx, s.ID, &agents.StreamEventsParams{Deltas: true}) {
	if err != nil {
		return err
	}
	if item.Delta != nil && item.Delta.Kind == agents.DeltaText {
		fmt.Print(item.Delta.Text)
	}
	if item.Event != nil && item.Event.Type == agents.EventAgentMessage {
		fmt.Println()
	}
}
```

Stop the loop, or cancel `ctx`, to stop following; either releases the
connection at once.

## Pagination

List methods return an `iter.Seq2` that fetches pages as your loop needs
them; a loop that stops early fetches nothing more:

```go
for s, err := range ag.ListSessions(ctx, &agents.ListSessionsParams{Status: agents.StatusIdle}) {
	if err != nil {
		return err
	}
	fmt.Println(s.ID)
}
```

To page yourself, call the `Page` variant, such as `ListSessionsPage`, and
pass `NextCursor` back as `Cursor`.

## Errors

A request the API refused returns a `*latere.Error` with the HTTP status,
the API's error code, a message, details and the request's id. Test for the
common cases with `errors.Is`, and read the rest with `errors.As`:

```go
s, err := ag.GetSession(ctx, id)
switch {
case errors.Is(err, latere.ErrNotFound):
	// no such session, or not one you may read
case errors.Is(err, latere.ErrRateLimited):
	// back off; see Error.RetryAfter
case err != nil:
	var apiErr *latere.Error
	if errors.As(err, &apiErr) {
		log.Printf("%s: %s (request %s)", apiErr.Code, apiErr.Message, apiErr.RequestID)
	}
	return err
}
```

A call cut off by its context returns an error matching the context's,
such as `context.DeadlineExceeded`. A call that could not get credentials,
because the key was revoked or a token source failed, returns a
`*latere.TokenError` whose `Err` says why. `Error.Body` holds the response as it was sent, up to 64 KiB. An answer
without the API's error envelope, such as a proxy's page, has the HTTP
status text as its `Message` and its own text only in `Body`. When a
refusal carries more than the error, `Body` holds that too; a trigger's
failed firing is one, and `FireTrigger` returns it decoded along with the
error. Include the request id when you report a problem.

## Retries

A request that failed on the network, or that the API answered with 408,
429, 500, 502, 503 or 504, is sent again up to twice, with exponential
backoff and jitter, and after the wait a `Retry-After` header asks for.
Only requests that are safe to repeat are retried: `GET`, `PUT` and
`DELETE`, a `PATCH` that only sets values, such as `UpdateSession`, and a
`POST` that carries an idempotency key, which the API reads on `POST`
alone. The methods that create or send something send a key, generated
when you give none, so a retried `CreateSession` makes one session, not
two. These calls are sent once instead, never again after a failure:

| Call | Why |
|---|---|
| `agents.FireTrigger` | the API forgets a firing's key when it answers with a server error |
| `apps.CreateApp`, `apps.Invite` | the apps API takes no key yet |
| `connections.ConfirmConnection` | a finished sign-in answers 410 to a repeat |
| `connections.ReconnectConnection` | a repeat replaces the sign-in the first one started |
| `llm.Generate`, `llm.Stream` | the model may have run and been billed |

A repeat that reaches the API while the first request is still being
answered gets 409 (`latere.ErrConflict`) even though the first succeeds;
read the resource to learn the outcome. A request you send with
`Config.Do` follows its method, or `latere.Request.Retry`:
`latere.RetryAlways` for one that is safe to repeat whatever its method,
`latere.RetryNever` for one that is not.

A 401 is retried once with a fresh token when the token source can get one:
keys, `latere.NewActorTokens` and `signin`'s sources can, and a source of
your own can by implementing `latere.Resetter`. A credential the issuer
refuses, such as a revoked key, is not retried.

## Configuration

| Option | Default |
|---|---|
| `WithKey`, `WithTokenSource` | the key in `LATERE_KEY` |
| `WithBaseURL` | `LATERE_BASE_URL`, else `https://api.latere.ai` |
| `WithHTTPClient` | `http.DefaultClient` |
| `WithMaxRetries` | 2 |
| `WithBackoff` | 500ms, doubling up to 30s |
| `WithTimeout` | 80s for a call and its retries when its context has no deadline; a stream only until it connects; 0 turns it off |
| `WithUserAgent` | adds your product before the SDK's own token |
| `WithHooks` | none; called before and after every attempt |

For tracing or metrics, pass an `*http.Client` whose transport is
instrumented, such as one wrapped by OpenTelemetry's `otelhttp`. The token
exchanges of a key or a sign-in go through the same client and hooks. The
SDK keeps no global state: build as many configurations as you need.

To call an endpoint the packages do not cover yet, use `Config.Do` with a
`latere.Request`; it signs, retries and decodes as the packages do.

## Signing a person in

Package `signin` signs a person in from your program, in their browser
(`Browser`) or with a code they enter on another device (`Device`), then
exchanges their sign-in for API tokens:

```go
signer := &signin.Config{ClientID: "your-client-id"}
tok, err := signer.Browser(ctx, openInBrowser) // or signer.Device in a terminal
if err != nil {
	return err
}
tokens, err := latere.NewActorTokens(signer.TokenSource(tok, saveRefreshToken))
if err != nil {
	return err
}
cfg, err := latere.NewConfig(latere.WithTokenSource(tokens))
```

This needs a client id that Latere registers for your program;
registration is not self-service yet. To ask for one, write to
[contact@latere.ai](mailto:contact@latere.ai) with your program's name,
what it does, and which flows it uses. The client is registered with
`api.latere.ai` among its audiences, so its sign-ins can be exchanged for
API tokens, and, for `Browser`, with exactly the redirect address
`http://127.0.0.1/callback`. `Browser` picks a free port each time it
runs, and the sign-in service accepts any port on that address, but the
path must be `/callback`.

## Versioning

The SDK is at v0: its API may still change between minor versions, and
every change that breaks code is called out in the
[changelog](CHANGELOG.md). From v1 it will follow semantic versioning. The
API under `/v1` only grows, so an older SDK keeps working against it.

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md).
To report a security problem, see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
