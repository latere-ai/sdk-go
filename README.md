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
| [`agents`](https://pkg.go.dev/latere.ai/x/sdk-go/agents) | Sessions with agents: create, list, count, follow, answer, approve, end, read what they made, and the rules a page an agent made runs under; shared conversations; triggers that start them |
| [`agents/routine`](https://pkg.go.dev/latere.ai/x/sdk-go/agents/routine) | A routine's trigger shape alone, for code that writes or reads a routine without calling the API |
| [`connections`](https://pkg.go.dev/latere.ai/x/sdk-go/connections) | The services a person connects once, such as a calendar or an MCP server, which agents then use |
| [`storage`](https://pkg.go.dev/latere.ai/x/sdk-go/storage) | Files in Latere Storage: upload a file of any size, read, list, move, delete, and link to it; what others shared with you |
| [`account`](https://pkg.go.dev/latere.ai/x/sdk-go/account) | Your Effort levels, wallet and payment pages, preferences, pins and bookmarks, share links, projects, trigger links and addresses, and computers |
| [`memory`](https://pkg.go.dev/latere.ai/x/sdk-go/memory) | What Latere remembers about a person across conversations: list, add, change, move, delete, restore, export and import memories, and the memory settings |
| [`apps`](https://pkg.go.dev/latere.ai/x/sdk-go/apps) | Apps: create, list, change, delete; their deploys, build logs, pictures, releases, visits, gallery listing and who may open them |
| [`evals`](https://pkg.go.dev/latere.ai/x/sdk-go/evals) | Evals: the definitions, experiments, cells and comparisons of experiments on agents, and an experiment followed as it runs |
| [`llm`](https://pkg.go.dev/latere.ai/x/sdk-go/llm) | Calling models in the Lux dialect: generate, stream, count tokens, list models |
| [`llm/wire`](https://pkg.go.dev/latere.ai/x/sdk-go/llm/wire) | The Lux dialect's types alone, for code that reads or writes the format without calling the API |
| [`web`](https://pkg.go.dev/latere.ai/x/sdk-go/web) | What the platform reads from the web for a program that shows an answer: a site's icon and the pictures an answer shows |
| [`signin`](https://pkg.go.dev/latere.ai/x/sdk-go/signin) | Signing a person in, for apps Latere registers |
| [`mls`](https://pkg.go.dev/latere.ai/x/sdk-go/mls) | Messaging Layer Security (RFC 9420): end-to-end encrypted groups whose members' devices hold the keys, and a view of a group for the server that delivers it |
| [`attachment`](https://pkg.go.dev/latere.ai/x/sdk-go/attachment) | The file a message carries, the one type a session's messages and a conversation between people share: its name, media type, size, digest, where it is kept and its preview |
| [`messaging`](https://pkg.go.dev/latere.ai/x/sdk-go/messaging) | Conversations between people over `mls`: the rules every device and the service check each change against, a person's devices and their vouches, history shared with someone let in, files encrypted on the device, the recovery key and the backup it unlocks |

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

An agent may offer the person standing work: a held call of its
`trigger` tool that proposes a routine or a trigger that fires on events,
or changes, resumes or deletes one, or a held call of its `wake` tool to
keep looking again after many looks in a row. `ToolUse.Offer` reads what
a client words it from: its `Action`, the name, the ask, the agent's
sentence of why, the schedule or the events, how it notifies, and the
trigger a change, a resume or a delete acts on. Only the person who
started the session answers it, with `ConfirmTool`: `DecisionAllow` runs
it as theirs, and `DecisionDeny` with a `Note` declines it in their
words.

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

A routine is an instruction a person hands an agent once, which runs with
nobody waiting: on a schedule, each time another app sends to its private
link or an email reaches its address, or at each of a few dates the person
picked. It is a trigger written in one shape, so every program that shows a
person's routines reads and changes the same ones; Latere Chat's are those
of its managed agent, `latere-chat`.

```go
r, err := ag.CreateRoutine(ctx, &agents.CreateRoutineParams{
	Agent:           "latere-chat",
	Title:           "Weekly news",
	Instruction:     "Summarize this week's news on climate policy.",
	Schedule:        &agents.RoutineSchedule{Days: []time.Weekday{time.Monday, time.Thursday}, Hour: 9, Zone: "Europe/Berlin"},
	PerWeekMicroUSD: 1_000_000, // at most $1 a week
	IdempotencyKey:  key,       // the same key never makes a second routine
})
if errors.Is(err, agents.ErrRoutineTitleMissing) {
	// say so to the person; nothing was sent
}

paused := true
_, err = ag.ChangeRoutine(ctx, "latere-chat", r.Name, &agents.ChangeRoutineParams{Paused: &paused})
```

A routine with `Starts: agents.StartsOnDates` runs once at each of its
`Dates`, times a schedule cannot say, one of them or up to
`agents.MaxRoutineDates`, and the API deletes it after the last. At least
one date must be to come; one that has passed may stay in the list and
never runs, so a change can write back the dates it read. `DatesZone` is
the zone the person picked them in, kept so the routine read back says the
dates as they picked them, and `r.NextRunAt` is the first date to come.
`Model` is the model each run's session starts on,
such as `"tier/thorough"`, in place of the agent's own, and
`Notify: agents.NotifyJudged` brings a run's result to the person only
when the agent judges it needs them. A change that names none of these
keeps what the routine has.

`ListRoutines` lists them, `GetRoutine` reads one, `DeleteRoutine` deletes
one and `RunRoutine` runs one now. A change reads the trigger and writes
back every field it does not name as the API answered it, those this SDK
does not model included. The name, the instruction, the schedule and the
dates are checked before anything is sent, each refusal an error to match with
`errors.Is`, such as `agents.ErrRoutineInstructionTooLong`; a name that
names no routine matches `latere.ErrNotFound`. The agent must exist in the
caller's context first: a program signed in as a person makes a managed
agent exist with `account.ApplyManagedAgent`. A routine's runs are the
sessions `ListSessions` lists with `Trigger: r.TriggerID`.

A routine may have a weekly allowance, `PerWeekMicroUSD`, in millionths
of a US dollar: the most its runs, and the looks of the wakes set in the
sessions it started, may cost in a rolling 7 days. The API counts that
spend at each firing, and a firing while it is at the allowance starts
nothing and records `agents.FiringSkippedBudget`; once the oldest runs
leave the 7 days, the next firing runs, with nothing to resume. A change
that names no allowance keeps the one the routine has, and zero takes it
off. `r.Spend` is the routine's week as of its latest run, a
`TriggerSpend`: what it cost, the allowance, a run's mean cost and what
the window is on course to cost, with `AheadOfPace` and `AtAllowance`
to say how it stands. `r.SkippedBudget` counts the runs the allowance
skipped.

The shape itself, the manifest, the name a key makes and the checks, is
package `agents/routine`, which imports the standard library alone. A
server that applies a routine on a person's behalf, such as one an agent
offered and the person accepted, writes it with `routine.Manifest`, so
what it makes is a routine every client reads as one, and
`routine.WriteAllowance` writes an allowance into any trigger's spec. The
package holds
the other shape an offer is written in too: an offer on events no
routine starts from (`routine.StartsOn` says which) is named
`routine.EventName(routine.OfferKey(session, call))`, its message is
`routine.EventMessage(ask)`, and `routine.ReadEvent` reads it back.

### Watches

Beyond the routines a person makes, an agent keeps standing work of its
own: a routine or an event trigger it offered and the person accepted,
and a wake, a trigger that fires once and sends the agent's note back to
the session it set it in. `ListWatches` reads all of them as `Watch`es,
each with its kind, when it looks next, the sentence of why it exists,
who set it, the session it came from, whether it is paused, a wake's
place in its row of looks, and the week of the trigger its looks count
toward:

```go
whys := map[string]string{} // keep between readings
for w, err := range ag.ListWatches(ctx, &agents.ListWatchesParams{Agent: "latere-chat", KnownWhys: whys}) {
	if err != nil {
		return err
	}
	whys[w.TriggerID] = w.Why
	fmt.Println(w.Kind, w.Title, w.Why, w.SetBy, w.NextAt)
	if w.Spend.AheadOfPace() {
		fmt.Println("on course past its allowance of", w.PerWeekMicroUSD)
	}
}

hold, err := ag.SetTriggerHold(ctx, true) // pause everything at once
```

A page of watches reads one page of triggers, each session a watch came
from once, and, for watches an agent offered, the log of their session
from its start up to the calls they were offered with, since that is
where the agent's sentence of why is kept. Pass the whys of the last
reading in `KnownWhys` and a reading reads no log again. An event
trigger an agent offered on events no routine starts from, such as a
repository's pushes, carries its agent's label under a name of
`routine.EventName`'s shape, so `ListRoutines` leaves it out and
`ListWatches` reads it. Pause or
resume any watch with `SuspendTrigger`, which keeps every other field of
its trigger, set or take off its weekly allowance with
`SetTriggerAllowance` the same way, and delete one with `DeleteTrigger`.
A paused watch's `Pause` says who paused it and when, and for one an
agent paused itself (`Pause.ByAgent()`), the sentence it gave and the
session it did so in.

A watch's looks count toward one trigger's allowance, `w.CountsToward`:
its own, for a routine or an event trigger, and for a wake, the trigger
that started the wake's session, none when a person started it. Its
`PerWeekMicroUSD` and `Spend` are that trigger's, so a wake shows the
week of the routine whose conversation it looks again in; that trigger
is read from the page, or once per page when it is on another. A
woken turn's `UserMessage` carries the same week as a `FiringSpend`, as
the firing found it, which the agent read before the message. An offer
read with `ToolUse.Offer` carries the allowance the agent asked for, its
estimate of a week of looks, as it wrote it (`PerWeekUSD`, such as
`"1.50"`) and in millionths of a dollar (`PerWeekMicroUSD`), for the
person to read before they allow it. `GetTriggerHold` and
`SetTriggerHold` read and set the hold of every trigger of the person,
which leaves each one's own pause as it was. A `Session` names its pending `Wake`, the `Outcome` of its
latest turn a trigger began, `agents.TurnQuiet` or `agents.TurnNotified`,
and the latest `Notice` its agent brought the person; the idle
`SessionStatus` that ends such a turn carries the outcome too. A
`ToolUse` and an `ApprovalRequested` say whether a person began their
turn, `Attended`: false on a turn a trigger or a wake began, which
nobody watches.

A `Session`'s `ContextReport` says how full its context is and what
fills it, from the latest model request of its own conversation; each
`EventModelRequest`'s `ModelRequest` carries the same report for that
request as its `Context`:

```go
if full, ok := s.ContextReport.UsedFraction(); ok {
	fmt.Printf("%.0f%% of %d tokens\n", full*100, s.ContextReport.Window)
	for _, p := range s.ContextReport.PartsInOrder() {
		fmt.Println(p.Part, p.Tokens) // instructions, tools, messages, files, summary, free
	}
}
```

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

`GetSessionSummary` counts the sessions a list would page through, by what
each is doing: running, waiting for a person's approval, idle, ended.

### Shared conversations

A person shares a conversation with a link, which `account.CreateShare`
makes. Anyone holding the link reads what it shows by the link's token,
the last part of its address: `GetSharedPage` reads a page of the events a
reader sees with the share's title, `ListSharedEvents` walks them all, and
`ReadSharedBlob` opens a file an answer kept. A program with no credential
of its own, such as a server that renders a link's page for a visitor,
builds its `Config` with `latere.WithoutCredentials`:

```go
cfg, err := latere.NewConfig(latere.WithoutCredentials())
if err != nil {
	return err
}
page, err := agents.New(cfg).GetSharedPage(ctx, token, nil)
if err != nil {
	return err // latere.ErrNotFound for a link that does not work
}
fmt.Println(page.Share.Title, len(page.Events))
```

The read sends the `Config`'s credential when it has one, which a share
whose audience is an organization's members would read.

The package covers sessions, their events and files, shared
conversations, triggers, routines and watches. Defining agents is not covered yet;
call it with `Config.Do` (see [Configuration](#configuration)).

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
		show(f.Path, r.MediaType(), r.Size(), r) // the file's own type and length, as f.MediaType and f.Size
		r.Close()
	}
}

r, err := ag.ReadSessionFile(ctx, s.ID, "report.md")
if errors.Is(err, agents.ErrFileUnavailable) {
	// the session's machine is not running; read it once the session runs again
}
```

Each returns a reader that is yours to close, with the file's own media
type and its length in bytes before you read any of it: a copy's are the
ones its record names, and a working file's are its name's or its leading
bytes' type and its length when the read began. A copy that is no PNG,
JPEG, GIF or WebP picture is answered as a download, an HTML page and an
SVG included, so show one only in a sandbox of your own. The
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

The package also keeps what a person's clients share:

- **Links that share a conversation:** `CreateShare` makes one,
  `ListShares` lists them, `UpdateShare` moves one to the last finished
  turn and `DeleteShare` stops it. `CreateShare` is sent once.
- **Projects**, which group conversations and hold the apps they work on:
  `ListProjects`, `CreateProject`, `GetProject`, `UpdateProject`,
  `DeleteProject`, `AddProjectApp` and `RemoveProjectApp`. `CreateProject`
  is sent once.
- **Preferences:** `UpdatePreferences` sends a merge patch, and the lists
  inside them change one item at a time: `AddSite` and `RemoveSite` for
  the network's sites, `BookmarkTurn` and `UnbookmarkTurn`, and `PinApp`,
  `UnpinApp`, `PinProject` and `UnpinProject` beside `PinSession`.
- **A routine's ways in from outside:** `ListTriggerInbound` lists the
  links and email addresses the person's triggers have, and
  `IssueTriggerHook` and `IssueTriggerAddress` give a trigger its own,
  replacing the one it had, with `RemoveTriggerHook` and
  `RemoveTriggerAddress` to turn them off.
- **Money and data:** `StartCheckout` answers a payment page for credit,
  sent once, `GetSpend` what the person spent by day, split into what
  they led and what ran with nobody watching (`PersonLedMicroUSD` and
  `ProactiveMicroUSD`), with the share of their credit that goes to the
  latter, its pool and what it spent in the last 7 days,
  `GetDataPlaces` where the installation keeps sessions and runs their
  machines, and `GetModelDataTerms` the data terms of models. `GetSpend`
  reads the personal context alone; an organization's token is refused
  with `latere.ErrForbidden`.
- **The month to its end:** `GetForecast` answers what the person will
  have spent by the end of the calendar month in a time zone, computed
  once on the server: every day of the month in both halves, spent so far
  and expected, the day their credit would run out (`RunsOutOn`), the day
  runs would start being skipped (`ProactiveSkipsFrom`), when a spent pool
  frees up (`ProactiveAgainAt`), and each trigger's month as a
  `ForecastItem` (`SpentMicroUSD`, `ForecastMicroUSD` or, before its first
  run, `MostMicroUSD`, its runs by day, its runs to come and the ones its
  allowance or the pool would skip, and `Skipping`). The items for which
  `InProactiveHalf` is true and `UnlistedProactiveMicroUSD` add up to the
  proactive half. A date is a `time.Time` at midnight UTC of the date in
  the forecast's zone. The personal context alone, as `GetSpend`.
- `RecordArrival` records the site that referred a person and the page
  they landed on.

```go
share, err := acct.CreateShare(ctx, s.ID)
if err != nil {
	return err
}
fmt.Println("anyone with", share.URL, "reads the conversation")
```

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

## Memory

Package `memory` is how a program shows a person what Latere remembers
about them across conversations, and lets them change it. Each memory is
one Markdown file in the person's Storage, in one of two kinds of scope:
You, which every conversation in their personal context reads, or one of
their projects, which only that project's conversations read.

```go
mem := memory.New(cfg)
list, err := mem.ListMemories(ctx) // the settings and every memory, without bodies
for _, group := range list.Limits.Types {
	for _, m := range list.Memories {
		if m.Type == group {
			fmt.Println(group, m.Name, m.Description)
		}
	}
}
m, err := mem.GetMemory(ctx, list.Memories[0].Ref()) // one memory, with its body
desc := "Keep lists under four things."
_, err = mem.UpdateMemory(ctx, m.Ref(), &memory.UpdateMemoryParams{Description: &desc, IfMatch: m.Checksum})
```

A `Ref` addresses one memory by its scope, its project and its id, and
`Memory.Ref` returns the one a list answered. A change or a delete that
names the checksum it read is refused with `memory.ErrChanged` when the
memory changed since, so it never overwrites what a conversation saved
in the meantime; `UpdateMemory` with `Scope` and `Project` moves a memory
between You and a project. The package also covers:

- `AddMemory` saves what the person typed, and is sent once.
- `DeleteMemory` deletes one memory for good, and `ForgetEverything`
  every memory of the person and their projects; a program asks the
  person to confirm first.
- `UpdateSettings` turns Remember new things and Include sensitive topics
  on or off. Turning sensitive topics off deletes every memory marked
  sensitive, and says how many.
- `ListForgotten` lists what conversations forgot in the last 30 days, and
  `RestoreMemory` brings one back. It is sent once.
- `ExportMemory` streams a zip of every memory as Markdown files; a zip
  that stops before its end ends the read with
  `memory.ErrExportIncomplete`, so a program asks again. `ImportMemory`
  saves what another assistant remembers, pasted as it answered the
  list's `ImportPrompt`, and leaves out what the scope holds already. It
  is sent once; after a failure, the same text again saves the rest.

The bounds a memory is held to, and the import prompt, come with the list
in `Limits` and `ImportPrompt`, so a program never writes its own. Memory
is kept for a person in their personal context: an organization's token
is refused with `memory.ErrPersonalOnly`, and a conversation's agent's
with `memory.ErrAgentToken`, since an agent changes memory with its file
tools. Each refusal code a program branches on has its own error to test
with `errors.Is`: `ErrNotFound`, `ErrTaken`, `ErrNameTaken`,
`ErrTooLarge`, `ErrInvalidField`, `ErrUnavailable` and
`ErrStorageUnavailable` beside the ones above.

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

`ReadThumbnail` opens a deploy's picture as a stream, with its media type,
length and how a cache may keep it, for a program that passes it on to a
browser. `ListAppSessions` lists the sessions whose publishes built an
app, `GetAppVisits` its daily page loads and visitors, `GetDeployByID` and
`GetDeployApp` read a deploy by its ID alone and the app it belongs to,
and `GetListing`, `RequestListing` and `WithdrawListing` handle its place
in the gallery. `CreateFramePass` answers the address that shows a private
app in a frame of another site's page, and `EndGateSessions` ends the
person's sign-ins at private apps as they sign out.

`GetApp`, `GetDeployApp` and `ReadThumbnail` also answer a `Config` built
with `latere.WithoutCredentials`, as anyone with an app's address reads
it: a public or unlisted app that is published, and nothing of any other.

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

`MoveFile` and `MoveFolder` rename a file, or a folder with everything
under it in one change, without copying a byte; both are sent once, since
a repeat would find nothing left to move. `DeleteFile` and `DeleteFolder`
move them to the space's trash, or remove them with
`DeleteOptions.Permanent`. `ListSharedWithMe` lists what other people
shared with you, each share naming the space and the path by which
`ReadFile` and `ListFiles` reach it.

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

An edited message is a fork of the session just before the message it
replaces, with the new message in the same call. A file the old message
carried is kept by its `Blob`, without its bytes being sent again:

```go
_, err = ag.ForkSession(ctx, id, &agents.ForkSessionParams{
	BeforeSeq:   old.Seq,
	Message:     []agents.ContentBlock{agents.Text("Summarize the talk in French.")},
	Attachments: []agents.NewAttachment{{Name: "talk.mp4", Blob: kept.Blob}},
})
```

## Evals

Package `evals` reads Evals, the platform's experiments on agents and on
programs built on models. An experiment runs a suite's tasks for each of
its candidates and compares them; its definitions, metrics, graders,
tasks, suites and candidates, are each kept in versions named by the
digest of their content, and a name reads the current one:

```go
ev := evals.New(cfg)
suite, err := ev.GetSuite(ctx, "fix-flaky-tests")
if err != nil {
	return err
}
for e, err := range ev.ListExperiments(ctx, &evals.ListExperimentsParams{Suite: suite.Digest}) {
	if err != nil {
		return err
	}
	fmt.Println(e.ID, e.State)
}
for event, err := range ev.StreamEvents(ctx, experimentID, nil) {
	if err != nil {
		return err
	}
	fmt.Println(event.Type) // a snapshot first, then cells, trials, gradings and comparisons
}
```

`StreamEvents` follows an experiment to its end, connecting again after a
dropped connection from the last event it delivered. `Version.Ref` gives
the name@digest that names one version exactly. The package reads;
applying definitions and creating, canceling and closing experiments are
not covered yet, and a comparison's figures are read from its `RawJSON`.

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

## Site icons and pictures of the web

A program that shows an answer draws each link with its site's icon, as
Latere Chat does. Package `web` reads the icon through the platform, which
fetches it from the site on its own outbound path, so the site never sees
the reader, and answers it as a small PNG:

```go
icon, err := web.New(cfg).GetIcon(ctx, "techcrunch.com", web.IconSmall)
switch {
case errors.Is(err, web.ErrInvalidHost):
	// an IP address, localhost or another name no public site has
case err != nil:
	return err
case icon == nil:
	// the site has no icon: draw the link alone
default:
	draw(icon.Data, icon.MediaType)
}
```

`IconSmall` is 32 pixels a side, for an icon beside a link's words;
`IconLarge` is 64. An icon is the site's and no one's, so the request
carries no credential. An answer past `web.MaxIconBytes`, 64 KiB, is
refused with an error matching `web.ErrIconTooLarge`. The platform keeps
an icon for a week and a site without one for a day, so a program that
keeps what it read need not ask sooner. A server that passes the icon on
to a browser sends it with `icon.CacheControl` and `icon.ETag`, the
platform's own policy.

A picture on another website that an answer shows is read through the
platform too, encoded again at most `web.ImageSmall`, 800 pixels, or
`web.ImageLarge`, 1600, wide. `ReadImage` reads it as the person the
`Config` speaks for, and `ReadSharedImage` as the reader of a share link,
for an address the shared answers name. Both answer an `ImageReader` that
streams the picture, so a server passes it on to a browser as it reads it:

```go
img, err := web.New(cfg).ReadImage(ctx, address, web.ImageSmall)
if errors.Is(err, web.ErrNoImage) {
	return nil // the platform shows no picture there: draw a placeholder
}
if err != nil {
	return err
}
defer img.Close()
w.Header().Set("Content-Type", img.MediaType())
w.Header().Set("Cache-Control", img.CacheControl())
_, err = io.Copy(w, img)
```

## The API's own answer

Every resource the `agents`, `account`, `apps`, `connections`, `evals` and
`storage` packages return keeps the JSON the API sent. `RawJSON` returns it with every field,
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
A `web.Icon` is a picture's bytes, not JSON, and has none either.

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
two. A few `POST`s whose repeat leaves what one would are retried without
a key: `account.IssueTriggerHook` and `IssueTriggerAddress`, whose repeat
replaces the link a lost answer issued, and `apps.CreateFramePass`, whose
unused pass lapses within a minute. These calls are sent once instead,
never again after a failure:

| Call | Why |
|---|---|
| `agents.FireTrigger` | the API forgets a firing's key when it answers with a server error |
| `agents.RunRoutine` | a routine's run is a firing of its trigger, sent as `FireTrigger` sends it |
| `account.CreateShare` | the account API takes no key on it; a repeat makes a second link |
| `account.CreateProject` | the account API takes no key on it; a repeat makes a second project |
| `account.StartCheckout` | a repeat opens a second payment page |
| `apps.CreateApp`, `apps.Invite` | the apps API takes no key yet |
| `connections.ConfirmConnection` | a finished sign-in answers 410 to a repeat |
| `connections.ReconnectConnection` | a repeat replaces the sign-in the first one started |
| `storage.StartUpload` | the storage API takes no key; an upload left open lapses by itself |
| `storage.MoveFile`, `storage.MoveFolder` | the storage API takes no key; a repeat finds nothing left to move |
| `llm.Generate`, `llm.Stream` | the model may have run and been billed |
| `memory.AddMemory` | the account API takes no key on it; a repeat saves the memory twice |
| `memory.ImportMemory` | the account API takes no key on it; calling it again with the same text saves what a failed import left out |
| `memory.RestoreMemory` | a repeat finds nothing left to restore |
| `memory.UpdateMemory`, when it moves a memory | a repeat finds nothing left to move; a change in place is retried |

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
| `WithKey`, `WithTokenSource`, `WithoutCredentials` | the key in `LATERE_KEY` |
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
`latere.Request`; it signs, retries and decodes as the packages do. A
route that reads no credential takes `Request.Anonymous`, which sends the
request without one. A program that holds no credential at all builds its
`Config` with `WithoutCredentials`, which sends none on any request: the
API's public reads, a shared conversation, an app anyone may open and its
pictures, and a site's icon, answer it, and every other call is refused
with 401. Those reads are limited per client address; a server that reads
for its visitors forwards each one's address in `X-Real-IP` from a
`WithHooks` `BeforeRequest`, which sees the call's context, where the
installation takes that header from the server.

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

With the same sign-in, before it is exchanged, `UserInfo` says who signed
in and the organization they act in, `ListOrganizations` lists the
organizations they belong to, and `GetHandle` reads the handle another
person claimed, by the id the sign-in service names them by. These two read
the sign-in service's own records and need no client id; a token for the
API is refused there with 401.

`SwitchOrganization` moves the sign-in to another organization the person
belongs to, or to their personal account, as a refresh that answers new
tokens. `CreateKey` creates a key for the person, which a program presents
with `latere.WithKey` to call the API as them without their sign-in, and
answers its value this once; it takes a sign-in through a client Latere
registers as first party. `ListKeys` lists the person's keys and
`RevokeKey` revokes one. `CreateKey` is sent once, since the sign-in
service takes no idempotency key and a repeat would create a second key.

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
