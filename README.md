# Latere Go SDK

[![Go Reference](https://pkg.go.dev/badge/latere.ai/x/sdk-go.svg)](https://pkg.go.dev/latere.ai/x/sdk-go)
[![CI](https://github.com/latere-ai/sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/latere-ai/sdk-go/actions/workflows/ci.yml)

The official Go client for the [Latere API](https://platform.latere.ai/docs):
start sessions with agents and follow them as they work, connect the
services they use, store files, publish apps, call models, and read your
account. It
depends on the Go standard library alone.

```sh
go get latere.ai/x/sdk-go
```

The SDK has one package per capability of the API, plus a small core that
they share. Import the ones you use:

| Package | What it does |
|---|---|
| [`latere`](https://pkg.go.dev/latere.ai/x/sdk-go/latere) | The core: configuration, credentials, errors, retries, pagination |
| [`agents`](https://pkg.go.dev/latere.ai/x/sdk-go/agents) | Sessions with agents: create, list, follow, answer, approve, end, read what they made; triggers that start them |
| [`connections`](https://pkg.go.dev/latere.ai/x/sdk-go/connections) | The services a person connects once, such as a calendar or an MCP server, which agents then use |
| [`storage`](https://pkg.go.dev/latere.ai/x/sdk-go/storage) | Files in Latere Storage: upload a file of any size, read, list, and link to it |
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

More packages will follow the same pattern, such as repos and
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

### Routines

A routine is an instruction a person hands an agent once, which runs again
and again with nobody waiting: on a schedule, or each time another app
sends to its private link or an email reaches its address. It is a trigger
written in one shape, so every program that shows a person's routines reads
and changes the same ones; Latere Chat's are those of its managed agent,
`latere-chat`.

```go
r, err := ag.CreateRoutine(ctx, &agents.CreateRoutineParams{
	Agent:          "latere-chat",
	Title:          "Weekly news",
	Instruction:    "Summarize this week's news on climate policy.",
	Schedule:       &agents.RoutineSchedule{Days: []time.Weekday{time.Monday, time.Thursday}, Hour: 9, Zone: "Europe/Berlin"},
	IdempotencyKey: key, // the same key never makes a second routine
})
if errors.Is(err, agents.ErrRoutineTitleMissing) {
	// say so to the person; nothing was sent
}

paused := true
_, err = ag.ChangeRoutine(ctx, "latere-chat", r.Name, &agents.ChangeRoutineParams{Paused: &paused})
```

`ListRoutines` lists them, `GetRoutine` reads one, `DeleteRoutine` deletes
one and `RunRoutine` runs one now. A change reads the trigger and writes
back every field it does not name as the API answered it, those this SDK
does not model included. The name, the instruction and the schedule are
checked before anything is sent, each refusal an error to match with
`errors.Is`, such as `agents.ErrRoutineInstructionTooLong`; a name that
names no routine matches `latere.ErrNotFound`. The agent must exist in the
caller's context first: a program signed in as a person makes a managed
agent exist with `account.ApplyManagedAgent`. A routine's runs are the
sessions `ListSessions` lists with `Trigger: r.TriggerID`.

### Searching

`SearchSessions` finds the sessions whose messages, answers and answers to
questions hold every word of a query, each with its newest matching events
and an excerpt whose matched words are marked:

```go
for found, err := range ag.SearchSessions(ctx, &agents.SearchSessionsParams{Query: "release notes", Agent: "latere-chat"}) {
	if err != nil {
		return err
	}
	fmt.Println(found.Session.Title, found.Matches[0].Excerpt)
}
```

The package covers sessions, their events and files, triggers and
routines. Defining agents and counting sessions are not covered yet; call
them with `Config.Do` (see [Configuration](#configuration)).

### What an agent made

A session keeps a copy of each picture or file of its machine that an
answer shows or links, and an `agents.EventFilesKept` after the answer
names them. `ReadBlob` reads a copy by its digest, also after the session
ended; `ReadSessionFile` reads any file of the session's working directory
as it is now, while the session's machine runs:

```go
for e, err := range ag.ListEvents(ctx, s.ID, nil) {
	if err != nil {
		return err
	}
	if e.Type != agents.EventFilesKept {
		continue
	}
	var kept agents.FilesKept
	if err := e.Decode(&kept); err != nil {
		return err
	}
	for _, f := range kept.Files {
		r, err := ag.ReadBlob(ctx, s.ID, f.Blob)
		if err != nil {
			return err
		}
		show(f.Path, f.MediaType, r) // f.MediaType is the file's own type
		r.Close()
	}
}

r, err := ag.ReadSessionFile(ctx, s.ID, "report.md")
if errors.Is(err, agents.ErrFileUnavailable) {
	// the session's machine is not running; read it once the session runs again
}
```

Each returns a reader that is yours to close. Its media type is the one the
API answered: a copy that is no PNG, JPEG, GIF or WebP picture is answered
as `application/octet-stream`, and its record names its own type. The
`Config`'s timeout bounds the wait for the answer and nothing after it, so
a large file reads for as long as `ctx` allows. `ReadSessionFile` refuses a
file past `agents.MaxSessionFileBytes`, 32 MiB, with an error matching
`agents.ErrFileTooLarge`.

The results of the agent's tool calls record what else it made. A
`ToolResult`'s `Meta` names the file a write or an edit left, with its
`Size`, and a publish's `PublishRecord`: the app, its address, the
preview, the deploy and its `PublishStatus`. An
`agents.EventSessionMachine` names the machine's working directory, which
those files are inside:

```go
switch e.Type {
case agents.EventSessionMachine:
	var m agents.SessionMachine
	if err := e.Decode(&m); err != nil {
		return err
	}
	workdir = m.Machine.Workdir
case agents.EventToolResult:
	var r agents.ToolResult
	if err := e.Decode(&r); err != nil {
		return err
	}
	if r.Meta != nil && r.Meta.Size != nil {
		fmt.Println("made", strings.TrimPrefix(r.Meta.Path, workdir+"/"), *r.Meta.Size, "bytes")
	}
}
```

A session that keeps its files in Storage names the folder in
`Session.Folder`. A kept file whose `Ref` is set is at
`Folder.Path + "/" + Ref` in the `Folder.Owner` space, where package
`storage` reads it with its own media type and size.

## Account

```go
acct := account.New(cfg)
levels, err := acct.GetEffortLevels(ctx) // what each Effort level costs now
devices, err := acct.ListDevices(ctx)    // the person's computers sessions can work on
_, err = acct.RenameDevice(ctx, id, "Work laptop") // id is a Device's ID
err = acct.RemoveDevice(ctx, id)
_, err = acct.PinSession(ctx, s.ID)
```

A person's computers are managed with their own sign-in; a key's token is
refused with 403. Removing a computer the person no longer holds succeeds,
so a removal is safe to repeat.

### Serving sessions on a computer

A program that connects a person's computer and serves sessions on it,
such as one built on Cella's device engine, `latere.ai/x/cella/device`,
takes the Latere values it needs from the SDK rather than writing them
down:

| What | Where |
|---|---|
| The labels a create in Environments carries in the token's context | `account.GetEnvironmentsContext` |
| The label that marks a computer's environment | `account.LabelKind` = `account.KindDevice` |
| The labels and annotation of a session's sandbox | `agents.LabelSession`, `LabelAgent`, `LabelAgentVersion`, `LabelTrigger`, `LabelTriggerFiresOn`, `AnnotationAgentName` |
| The audience of an API token | `latere.DefaultAudience` |
| Whom a handed token names, its context and its expiry | `latere.ReadUnverifiedClaims` |

`ReadUnverifiedClaims` reads a token without verifying it: what it returns
is as true as the source the token came from, and the API, which the token
is presented to, verifies it.

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

## Files

Package `storage` reads and writes the files of a person's or an
organization's Storage, which agents and Latere's apps read and write too.
`UploadFile` takes a file of any size up to 5 GiB: in one request up to
16 MiB, and above that in parts put straight to object storage, four at a
time, reporting its progress as it goes:

```go
f, err := os.Open("talk.mp4")
if err != nil {
	return err
}
defer f.Close()
info, err := f.Stat()
if err != nil {
	return err
}
st := storage.New(cfg)
file, err := st.UploadFile(ctx, storage.Me, "files/videos/talk.mp4", f, info.Size(), &storage.UploadOptions{
	ContentType: "video/mp4",
})
```

`ReadFile` streams a file back, `ListFiles` walks a folder, and
`GetDownloadURL` answers a link that serves the file without a credential
for five minutes; a public file's link without `Attachment` does not
expire. A link is a credential while it lasts, and the SDK never puts one
in an error.

Sending a file's bytes takes as long as the network allows, so each
attempt of a write or a part has the timeout and two minutes for every
16 MiB, and an attempt cut off is sent again. An upload in parts that
fails once it is open is given up, and its error is a
`*storage.UploadError` naming it.

A message to a session carries pictures for the model to see and files for
the agent to work on. A file of up to 5 MiB goes with the message; a larger
one goes up to Storage first and the message names it there:

```go
_, err = ag.SendMessage(ctx, id, &agents.SendMessageParams{
	Content: []agents.ContentBlock{agents.Text("Summarize the talk.")},
	Attachments: []agents.NewAttachment{
		{Name: "talk.mp4", Source: &agents.AttachmentSource{Owner: storage.Me, Path: file.Path}},
	},
})
```

## Models

Package `llm` calls models in the Lux dialect, one request, response and
streaming shape for every catalog model. The models capability takes the
key itself rather than a token exchanged for it, which the package handles:
build its Client from a `Config` whose credentials are a key.

```go
models := llm.New(cfg)
res, err := models.Generate(ctx, &llm.Request{
	Model:    "anthropic/claude-sonnet-4.5",
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

Every resource the `agents`, `account`, `apps`, `connections` and `storage`
packages return keeps the JSON the API sent. `RawJSON` returns it with every field,
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
error. A refusal whose status alone does not tell it apart has an error
of its package's that `errors.Is` matches beside the status's:
`agents.ErrFileUnavailable` is a 409 that says the session's machine is
not running. Include the request id when you report a problem.

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
| `agents.RunRoutine` | a routine's run is a firing of its trigger, sent as `FireTrigger` sends it |
| `apps.CreateApp`, `apps.Invite` | the apps API takes no key yet |
| `connections.ConfirmConnection` | a finished sign-in answers 410 to a repeat |
| `connections.ReconnectConnection` | a repeat replaces the sign-in the first one started |
| `storage.StartUpload` | the storage API takes no key; an upload left open lapses by itself |
| `llm.Generate`, `llm.Stream` | the model may have run and been billed |

A repeat that reaches the API while the first request is still being
answered gets 409 (`latere.ErrConflict`) even though the first succeeds;
read the resource to learn the outcome. A request you send with
`Config.Do` follows its method, or `latere.Request.Retry`:
`latere.RetryAlways` for one that is safe to repeat whatever its method,
`latere.RetryNever` for one that is not, and `latere.RetryAnswers` for one
the API can resume but not repeat: it is sent again after an answer that
says it failed, never after a failure on the network.
`storage.CompleteUpload` is sent that way, since the API keeps an upload
until it completes and a completed one is gone.

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
| `WithTimeout` | 80s for a call and its retries when its context has no deadline; a stream only until it connects; each attempt of a file's bytes, the timeout and two minutes per 16 MiB; 0 turns it off |
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
