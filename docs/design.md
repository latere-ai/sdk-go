# How the SDK is designed

This page explains why the SDK is shaped the way it is. Read it before you
propose a change to how requests, retries, credentials, pages or streams
work; the README says how to use them.

## Goals

- **The standard library alone.** A program that imports the SDK pulls in
  no other module, so it adds nothing to a dependency review and cannot
  conflict with the versions a program already uses.
- **One package per capability.** A program imports `agents` without
  compiling the code for storage or apps, and each package's docs read on
  their own.
- **No global state.** Everything a request needs comes from a
  `latere.Config` the caller built; two configurations in one program do
  not affect each other.
- **A context on every call**, so a caller can cancel and bound any
  request, stream or list.

## Layout

```mermaid
flowchart TD
    agents --> latere
    account --> latere
    apps --> latere
    connections --> latere
    storage --> latere
    llm --> latere
    llm --> wire[llm/wire]
    signin --> latere
    latere --> sse[internal/sse]
    app[your program] --> agents
    app --> account
    app --> apps
    app --> connections
    app --> storage
    app --> llm
    app -. a person's sign-in .-> signin
    gateway[the Lux gateway's codec] --> wire
```

`latere` is the core: configuration, credentials, the error type, the
request loop, pagination and streams. A capability package (`agents`,
`account`, `apps`, `connections`, `llm`, `storage`, and those to come) holds typed
models and one method per endpoint, and calls the API only through
`Config.Do`, `Config.Open`, `Config.Stream` and `latere.Items`. `signin` is separate
because most programs, those that run with a key, never sign a person in;
the core does not import it.

A capability package's client is `New(cfg)`, with one method per endpoint
named verb first after the API's resource, as the AWS SDK names its
operations: `GetSession`, `ListSessions`, `CreateSession`, `GetWallet`. A
client exposes no service fields: a type has exported fields only when it
is plain data, such as a model or a request's parameters, or configuration
filled in once before use. A type with state, invariants or concurrent
use, such as a client, a configuration or a token source, keeps its fields
unexported behind a constructor and methods. Sharing one `Config` between
packages shares its HTTP client, credentials and retry policy.

A call on a `Config` that `NewConfig` did not build, a nil one included,
fails with an error saying so rather than panicking.

## Configuration

`latere.NewConfig` takes functional options, and an option can fail (a
malformed base URL, a negative retry count), so `NewConfig` returns an
error instead of panicking later. The token sources' constructors,
`NewKeyTokens` and `NewActorTokens`, do the same with their options. When
neither `WithKey` nor `WithTokenSource` is given, `NewConfig` reads
`LATERE_KEY`, and when `WithBaseURL` is absent it reads `LATERE_BASE_URL`;
an explicit option always wins over the environment.

Per-call options are not offered. A call that needs a shorter deadline
uses its context; a part of a program that needs another retry policy
builds a second `Config`.

## Credentials

Every request carries a bearer token from a `latere.TokenSource`, an
interface of one method, `Token(ctx)`. Three sources ship with the SDK:

- `NewKeyTokens` exchanges a key from the console for a short-lived token
  with the OAuth client credentials grant, which is what the API's
  authentication docs describe. `WithKey` builds one.
- `NewActorTokens` turns a person's sign-in into short-lived API tokens.
- `StaticToken` sends a token the caller already holds.

The two exchanging sources send their exchanges through the HTTP client,
User-Agent and hooks of the `Config` they are given to, so a proxy, a
trace or a log sees them as it sees the requests they sign.

`ReuseToken` wraps a source so that a token is reused until shortly before
it expires, and so that concurrent requests needing a new token cause one
fetch, not one each. The fetch runs detached from the first caller's
context, so that if that caller gives up, the others still get the token,
and is bounded by `TokenTimeout` instead: a source that hangs fails the
callers waiting on it, and the next call asks again, rather than holding
every later request.

A source that also implements `Resetter` lets the core recover from a 401:
it drops its token, and the request is sent once more with a new one. A
401 means the API did not act on the request, so this retry is safe for
any method. A source's own failure is retried only when it may pass, on
the network or at an issuer that is down; a credential the issuer refuses
fails the call at once.

`golang.org/x/oauth2` would cover part of this, but it is a dependency,
and the API's token exchanges are small enough to write and test here.

## Requests and retries

`Config.Do` sends a request, decodes a 2xx answer into the caller's value,
and turns anything else into a `*latere.Error`.

A request is retried when the network failed or the API answered 408, 429,
500, 502, 503 or 504, and only when it is safe to send twice, which its
`RetryRule` says. The zero rule goes by the method: a `GET`, `HEAD`, `PUT`,
`DELETE` or `OPTIONS`, or a `POST` carrying an idempotency key. The API
reads `Idempotency-Key` on `POST` alone, so a key does not make any other
method safe to repeat; the rule is set on the request rather than inferred
from the key. A `PATCH` that only sets values, such as a session's title or
model, has the same effect sent twice and carries `RetryAlways`, as does
counting a request's tokens, which changes nothing.

Every method that creates or sends something by `POST` sends a key,
generated when the caller gives none, so retries never create a second
session or send a message twice. The calls a key cannot make safe carry
`RetryNever`, are sent once, and say so in their docs:

- `apps.CreateApp` and `apps.Invite`: the apps part takes no key yet, so a
  repeat could create a second app, or answer an invite's people as
  already invited and count them twice against the day's invitations.
- `agents.FireTrigger`: the API forgets a firing's key when it answers
  with a server error, so a repeat fires the trigger again. The caller may
  fire again with the same key.
- `connections.ConfirmConnection`: a finished sign-in answers a repeat
  with 410, which would hide that the first confirmation succeeded.
- `connections.ReconnectConnection`: a person signs in once at a time, and
  a repeat replaces the sign-in the first call started.
- `llm.Generate` and `llm.Stream`: see [the models
  capability](#the-models-capability).
- `storage.StartUpload`: the storage part takes no key, so a repeat could
  open a second upload. An upload left open lapses by itself after a day,
  and `UploadFile` gives up one that failed.

`storage.CompleteUpload` carries `RetryAnswers`, a rule between the two:
the storage part keeps an upload until it completes, so a completion it
answered with a server error is sent again and resumes, but one that
failed on the network is not, since a completed upload is gone and its
repeat would be refused with 404 though it succeeded; `StatFile` on the
path tells. `UploadFile` gives up an upload whose completion fails for
good, and returns an `*UploadError` naming it.

In each case the caller is better placed to decide. A repeat that arrives
while the first request is still being answered is refused with 409, which
the SDK does not retry, since it cannot tell it from a real conflict; the
first request still succeeds, and the docs tell the caller to read the
resource.

Most requests carry JSON. A file's bytes go as `Request.Content`, an
`io.ReaderAt` with its length, which the core reads again from the start
for each attempt, so a retried write sends the same bytes rather than a
drained stream, and the length goes ahead of the body, as the storage API
requires. A part of a large upload goes to the link object storage signed
for it, `Request.SignedURL`, which carries its own authority: the request
carries no credential there, and a 401 there is not answered by renewing
the token, and a request that also sets `SendKey` is refused. A call
whose answer is the link itself, such as `storage.GetDownloadURL`, keeps
the redirect with `Request.KeepRedirect`. `storage.ReadFile` keeps it
too and follows the one link itself, as a signed URL, to an https
address (http only when the API is reached over http, as on a machine of
one's own), and refuses a second redirect: Go's client would forward the
credential to the same host on another port and to its subdomains. A
signed link's query is its credential, so an error names a signed URL,
and any address of another host than the API's, by its scheme, host and
path alone, and never quotes a Location net/http could not parse.

The wait between attempts doubles from 500ms up to 30s, and is drawn at
random from the upper half of that range so that many clients failing
together do not retry together. A `Retry-After` header replaces the
computed wait. A `Retry-After` longer than the longest backoff ends the
retries and returns the error with `RetryAfter` set, so the caller decides
whether to wait that long.

A call whose context has no deadline is bounded by `DefaultTimeout`, 80
seconds as in Stripe's SDK, or by `WithTimeout`, for the call and all of
its retries, including reading the answer: a server that stops answering
cannot hold a program forever. A request that sends a file's bytes is the
exception: object storage answers a part only once its body is in, so a
bound meant for an answer would fail every slow upload. Each of its
attempts is bounded instead by the timeout and `TransferTime` for its
length, two minutes for every 16 MiB begun, the pace Latere Chat gives a
part, and an attempt cut off is sent again by its rule. The caller's own deadline is the more
specific choice and replaces the bound. A stream is bounded only until the
API's first answer, by a timer rather than a deadline, since the stream
then lasts as long as the session. A call cut off by its context, during
an attempt or a wait between attempts, returns an error matching the
context's, joined with the last answer when there was one.

## Errors

The API refuses a request with a status and a JSON envelope of a code, a
message and details. The SDK returns all of it in one type,
`*latere.Error`, together with the request id from `X-Request-Id`, which is
what support needs to find the request. `Error.Is` maps statuses to
sentinels (`ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`, `ErrConflict`,
`ErrPreconditionFailed`, `ErrRateLimited`), so the common checks need
`errors.Is` and no type assertion. An answer that is not the envelope, such as a proxy's error page, still
produces an `Error`, with the HTTP status text as its message: the page
was not written for the program's user, and a message is what a program
shows them. `Error.Body` keeps the whole response, up to the 64 KiB the
SDK reads, cut at a character boundary so it stays valid UTF-8. An
envelope cut at that limit still gives its code and message, read from
its start. `Body` also serves the few refusals that carry more than the
error: a trigger whose firing failed to
start its session answers 503 with the firing, and `FireTrigger` returns
that firing decoded beside the error.

A call that fails before any request is sent, because the token source
failed or the issuer refused the key, returns a `*TokenError` wrapping the
cause, after any retries. A program can then tell "the credentials are
wrong" from "the API refused this request"; an issuer's refusal inside it
is an `*Error`, so `errors.Is(err, ErrUnauthorized)` holds for a revoked
key either way.

## Pagination

A list endpoint answers a page of items and a cursor for the next one.
Each list method comes in two forms: `List` returns an `iter.Seq2[T, error]`
that fetches pages while the loop asks for items, and `ListPage` returns
one page for a caller that keeps the cursor itself, between requests of a
web app for instance. The iterator stops at an empty cursor and also at a
cursor equal to the last one, so a server answering the same page forever
cannot hang a loop.

## The models capability

The models capability authenticates a key as itself: a model client sends
one fixed string, and the service matches it by hash. So `latere.Request`
has `SendKey`, which sends the key a `Config` holds instead of the token it
is exchanged for, and package `llm` sets it on every request. A `Config`
whose credentials are a token source sends its token, which the service
accepts only when it is a key's value.

A generation is not idempotent: the model runs, and the call is billed.
`llm` sends it with `RetryNever`, so the core never sends it again, and a
generation's stream is read once, with no reconnect, unlike a session's
log: a dropped stream ends with `io.ErrUnexpectedEOF`. Counting tokens
changes nothing, so it carries `RetryAlways` and no key, which the service
would not read.

A request's labels follow the service's rule, at most eight pairs of a key
of letters, digits, `.`, `_` and `-` and a value that may also hold `:` and
`/`. The service drops a pair it cannot read; `llm` leaves such a pair out
before sending, so a label holding `,` or `=` cannot change another.

The Lux dialect's request, response and stream types are its one public
definition, and the service's own codec uses them, so a field cannot be
added on one side and missed on the other. They live in `llm/wire`, not in
`llm`, because the codec runs inside the gateway: importing `llm` would put
this module's HTTP client and token exchange in the gateway's build, where
its architecture admits no identity library. `wire` imports the standard
library alone. `llm` names each type as an alias and each constant again,
so a program imports one package, and a test fails when `wire` exports a
name `llm` does not.

## Streams

A session's events are a log with sequence numbers, served as server-sent
events whose `id` is the sequence. `Config.Stream` reads the frames, and
when the connection drops it reconnects with `Last-Event-ID` set to the
last frame it delivered, so the caller sees every event once, in order,
without writing reconnect code. A reconnect that reads no frame counts
against the retry limit; one that reads frames resets the count, so a long
stream survives many drops spread over hours. When the caller stops the
loop, the stream's body is closed without reading what is left, since a
live stream never ends on its own: draining it would wait for the next
keepalive, or forever, and hold one of the streams the API allows a
caller.

A frame is read whole, up to `sse.MaxLine`, 16 MiB, twice the largest body
the API takes. A larger one ends the stream with an error instead of a
reconnect, which would only read it again.

Whether a frame ends the stream is the capability's knowledge, not the
core's, so `Stream` takes a `done` predicate: `agents.StreamEvents` ends a
session's stream after the status event that says the session ended. Frames without
an id, such as the deltas of a response being written, are delivered but
do not move the resume point, because the server does not replay them.

## Models

The request and response types are written by hand. Hand-written types
let the docs say what each field means to a caller rather than repeat the
schema, give each value a program branches on a named type, and keep the
parameters of a call to what a caller sets, which a generator would not.

The tests hold the types to the API's documents, vendored as JSON in
`openapi/`: a field a model reads must be in its schema, a field the schema
marks as a date-time must be a `time.Time`, and every endpoint a package
calls must exist. Where a package holds a model to its whole schema, the
model reads every field the schema requires or has, and a field left out
on purpose is listed, with its reason, in the package's `spec_test.go`.
The `agents` tests also send each request the client makes, the fullest
and the least, and check its body against the schema of its route, and
check that each status, stop reason and event type the package declares
is one the document lists. The program that fetches and converts the
documents lives in its own module under `tools/`, so the YAML parser it
needs never reaches the SDK's `go.mod`. A weekly workflow fetches the
documents and fails when they changed, which is the signal to update the
models. The storage document names every route but gives the answers of
its file and upload routes no schema, so `storage`'s types follow the
API's reference pages, and its tests hold the routes it calls, and the one
list shape the document does define, to the document.

Every resource a package returns, a session, an event, a trigger, a wallet,
a pin, a device, an app, a connection, a file, an upload or a finished sign-in, keeps the bytes it was decoded from, returned by
`RawJSON`, as Stainless-generated SDKs and stripe-go do. A program that
shows or stores the API's answer, a command line's `--json` output or a
server that passes answers on, gets every field, including those added
after its SDK version was released, without waiting for the SDK to model
them. It is one rule for every package rather than a choice per package,
so a program never has to know which parts of the API it may relay as
they came. Parts of a resource, such as a session's budget, keep nothing:
the resource's JSON already holds them. A model a program builds itself
has none. The `llm` types are the exception: they are the Lux dialect's
own definition, shared with the gateway's codec, so they never lag what
the API sends.

## Observability

The SDK sends every request through the `*http.Client` it was given, so
tracing and metrics come from an instrumented transport, such as
OpenTelemetry's `otelhttp`, without the SDK depending on a telemetry
library. `WithHooks` adds two callbacks around each attempt, retries
included, for logging or counting what a transport cannot see, such as how
long an attempt took before a retry.

## Signing a person in

`signin` implements the two OAuth flows a native program uses. `Browser`
listens on a loopback port, sends the person to the sign-in page with a
PKCE challenge and a random state, and exchanges the code that comes back;
a return with the wrong state is refused and the flow keeps waiting for
the real one. `Device` shows a code for the person to enter on another
device and polls until they do, slowing down when the service asks.
`TokenSource` keeps a sign-in fresh with its refresh token and hands the
new token to a callback, so a program can store it where it keeps
secrets. Programs that only use keys never import the package.

Both flows need a client that Latere registers, with the API among its
audiences, until registration is self-service; the package documentation
says how to ask for one and what to register.

## What came from other SDKs

The design borrows from three widely used Go SDKs:

- **AWS SDK for Go v2:** one shared configuration handed to a client per
  service, operations as methods named verb first on that client, and
  exponential backoff with jitter. Not taken: the middleware
  stack, which is more machinery than an API of this size needs.
- **Stripe's Go SDK:** lists as `iter.Seq2`, an idempotency key generated
  for every `POST`, the request id on every error, two retries by default,
  and an 80-second default timeout. Not taken: the package-level key and backends,
  which are global state.
- **Anthropic's Go SDK:** retrying only requests that can be replayed,
  retrying on 408, 429 and 5xx answers, honoring `Retry-After`, a
  server-sent events stream consumed as an iterator, and the answer's JSON
  kept on each model for `RawJSON`. Not taken: optional
  parameter wrapper types and per-call request options; plain structs with
  `omitempty` fields and a context cover this API.

## Versioning

The SDK is at v0, and a minor version may break code; each break is marked
in the changelog. Once at v1 it follows semantic versioning, and a change
that breaks code needs a new major version under its own module path.
