# Latere Go SDK

[![Go Reference](https://pkg.go.dev/badge/latere.ai/x/sdk-go.svg)](https://pkg.go.dev/latere.ai/x/sdk-go)
[![CI](https://github.com/latere-ai/sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/latere-ai/sdk-go/actions/workflows/ci.yml)

The official Go client for the [Latere API](https://platform.latere.ai/docs):
start sessions with agents, follow them as they work, answer what they ask,
and read your account. It depends on the Go standard library alone.

```sh
go get latere.ai/x/sdk-go
```

The SDK has one package per capability of the API, plus a small core that
they share. Import the ones you use:

| Package | What it does |
|---|---|
| [`latere`](https://pkg.go.dev/latere.ai/x/sdk-go/latere) | The core: configuration, credentials, errors, retries, pagination |
| [`agents`](https://pkg.go.dev/latere.ai/x/sdk-go/agents) | Sessions with agents: create, list, follow, answer, approve |
| [`account`](https://pkg.go.dev/latere.ai/x/sdk-go/account) | Your Effort levels, wallet, preferences, pins and computers |
| [`signin`](https://pkg.go.dev/latere.ai/x/sdk-go/signin) | Signing a person in from a desktop app or a terminal |

Build one `latere.Config` and hand it to each package's `New`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

func main() {
	ctx := context.Background()
	cfg, err := latere.NewConfig(latere.WithKey(os.Getenv("LATERE_KEY")))
	if err != nil {
		log.Fatal(err)
	}

	w, err := account.New(cfg).Wallet(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%.2f USD left\n", float64(w.SpendableMicroUSD)/1e6)

	for s, err := range agents.New(cfg).Sessions.List(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.ID, s.Status, s.Title)
	}
}
```

More packages, for storage, apps, repos, environments, connections and
models, will follow the same pattern.

## Requirements

Go 1.26 or 1.27. The SDK supports the two newest Go releases, as Go itself
does.

## Authentication

Every request carries a short-lived bearer token. Choose where it comes
from:

- **A key.** Create one under **Keys** in the
  [console](https://platform.latere.ai/console), choosing what it may do,
  and give it with `latere.WithKey` or in the `LATERE_KEY` environment
  variable. The SDK exchanges the key for a token of about 15 minutes and
  renews it before it expires. Use a key for scripts, servers and CI.
- **A person's sign-in.** A desktop app or a command-line tool that acts
  for a person signs them in with package `signin`, then exchanges their
  sign-in for API tokens with `latere.ActorTokens`:

  ```go
  signer := &signin.Config{ClientID: "my-app"}
  tok, err := signer.Browser(ctx, openInBrowser) // or signer.Device for a terminal
  if err != nil {
  	return err
  }
  session := signer.TokenSource(tok, saveRefreshToken)
  cfg, err := latere.NewConfig(latere.WithTokenSource(&latere.ActorTokens{Session: session}))
  ```

  Your program needs a client id registered with Latere's sign-in service.
- **Your own.** Implement `latere.TokenSource`, or wrap a token you already
  hold with `latere.StaticToken`.

A request acts in the context its token names: a key's context, or the
organization a person chose when signing in.

## Agents

A session is a conversation with an agent. Every message, answer, tool call
and state change is an event in the session's log.

```go
sessions := agents.New(cfg).Sessions

s, err := sessions.Create(ctx, &agents.CreateSessionParams{
	Agent:   "release-notes",
	Message: "Write the release notes for v1.4.0.",
})

_, err = sessions.SendMessage(ctx, s.ID, &agents.MessageParams{
	Content: []agents.ContentBlock{agents.Text("Add a section for the breaking changes.")},
})
```

A session that stops to ask a question, or to have a tool call approved,
is idle with `StopReason` `agents.StopQuestion` or
`agents.StopToolConfirmation`. Answer with `sessions.Answer` or
`sessions.ConfirmTool`.

## Account

```go
acct := account.New(cfg)
levels, err := acct.EffortLevels(ctx)   // what each Effort level costs now
devices, err := acct.Devices(ctx)       // the person's computers sessions can work on
_, err = acct.PinSession(ctx, s.ID)
```

## Streaming

`Stream` follows a session's log as it grows, and ends after the event that
ends the session. When the connection drops it reconnects and resumes after
the last event it delivered, so you see each event exactly once. Ask for
deltas to show a response while the agent writes it:

```go
for item, err := range sessions.Stream(ctx, s.ID, &agents.StreamParams{Deltas: true}) {
	if err != nil {
		return err
	}
	if item.Delta != nil {
		fmt.Print(item.Delta.Text)
	}
	if item.Event != nil && item.Event.Type == agents.EventAgentMessage {
		fmt.Println()
	}
}
```

Stop the loop, or cancel `ctx`, to stop following.

## Pagination

List methods return an `iter.Seq2` that fetches pages as your loop needs
them; a loop that stops early fetches nothing more:

```go
for s, err := range sessions.List(ctx, &agents.ListSessionsParams{Status: agents.StatusIdle}) {
	if err != nil {
		return err
	}
	fmt.Println(s.ID)
}
```

To page yourself, call the `Page` variant and pass `NextCursor` back as
`Cursor`.

## Errors

A request the API refused returns a `*latere.Error` with the HTTP status,
the API's error code, a message, details and the request's id. Test for the
common cases with `errors.Is`, and read the rest with `errors.As`:

```go
s, err := sessions.Get(ctx, id)
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

Include the request id when you report a problem.

## Retries

A request that failed on the network, or that the API answered with 408,
429, 500, 502, 503 or 504, is sent again up to twice, with exponential
backoff and jitter, and after the wait a `Retry-After` header asks for.
Only requests that are safe to repeat are retried: `GET`, `PUT` and
`DELETE`, and requests that carry an idempotency key. Every method that
creates or sends something sends a key, generated when you give none, so a
retried `Create` makes one session, not two. A 401 is retried once with a
fresh token.

## Configuration

| Option | Default |
|---|---|
| `WithKey`, `WithTokenSource` | the key in `LATERE_KEY` |
| `WithBaseURL` | `LATERE_BASE_URL`, else `https://api.latere.ai` |
| `WithHTTPClient` | `http.DefaultClient` |
| `WithMaxRetries` | 2 |
| `WithBackoff` | 500ms, doubling up to 30s |
| `WithTimeout` | none; bounds a call and its retries when its context has no deadline |
| `WithUserAgent` | adds your product before the SDK's own token |
| `WithHooks` | none; called before and after every attempt |

For tracing or metrics, pass an `*http.Client` whose transport is
instrumented, such as one wrapped by OpenTelemetry's `otelhttp`. The SDK
keeps no global state: build as many configurations as you need.

To call an endpoint the packages do not cover yet, use `Config.Do` with a
`latere.Request`; it signs, retries and decodes as the packages do.

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
