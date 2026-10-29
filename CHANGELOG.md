# Changelog

All notable changes to this SDK are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the SDK
follows [semantic versioning](https://semver.org/); while it is at v0, a
minor version may break code, and every such change is marked **Breaking**.

## [Unreleased]

### Added

- Package `mls`: Messaging Layer Security, RFC 9420, in pure Go on the
  standard library, for the suites
  `MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519`,
  `MLS_128_DHKEMP256_AES128GCM_SHA256_P256`,
  `MLS_256_DHKEMP384_AES256GCM_SHA384_P384` and
  `MLS_256_DHKEMP521_AES256GCM_SHA512_P521`. It passes the IETF MLS
  working group's interoperability test vectors for them. A member
  creates a group (`CreateGroup`), joins with a Welcome (`JoinGroup`) or
  on its own from a group info (`JoinExternal`), stages commits that add
  and remove members (`Commit`, `Process`) and applies them with `Merge`,
  encrypts and opens messages (`Encrypt`, `Process`), and keeps its state
  between runs (`MarshalBinary`, `UnmarshalGroup`). `PublicGroup`
  follows a group for the server that delivers it, holding no key, with
  the proposals it keeps for a commit (`PendingProposals`).
  `VerifySignedContent` checks a message's original signature after it
  is shared with a member who joined later. The package builds for
  WebAssembly.
  The application decides which proposals a member keeps, seeing the
  group as it is (`Config.AcceptProposal`, `GroupState`), and whether a
  replacing leaf is the same member (`Config.CheckSuccessor`). A
  committer's Welcome carries extensions of its own to the joiners under
  its signature (`CommitOptions.WelcomeExtensions`,
  `Group.WelcomeExtensions`), a member that joins names the member
  whose group info it joined from (`Group.GroupInfoSigner`), and
  `PeekMessage` reads a group info's
  group, epoch and extension types. A staged commit's `Changes`
  names who proposed each change (`Proposals`) and which leaves it
  replaced (`Replaced`). `KeyPackage.Seal` and `KeyPackageSecrets.Open`
  encrypt to a key package's init key under a label of the
  application's, and `RequiredCapabilitiesExtension` builds the group
  context extension that names what every member must support. A group
  holds at most `MaxLeaves` leaves.
- Package `messaging`: conversations between people over `mls`. It
  holds the profile of a conversation's group (`Config`,
  `CreateOptions`, `KeyPackageOptions`), its people, owners and level as
  a group context extension (`State`), the rules each commit and
  proposal is checked against on a device and on the service's view
  alike (`Policy.CheckCommit`, `Policy.CheckProposal`, `Policy.Config`,
  `CheckMessage`), a person's devices as records vouched for by one
  another and signed by their own keys (`DeviceRecord`, `Directory`,
  `SecurityCode`), each naming its voucher under its own key so no one
  moves it under another root, with a device removed by a signed
  `Revocation` from a record under the same root, which the service can
  neither make nor undo and the apps keep as known
  (`Directory.Revocations`, `Directory.CheckRevocation`), with the checks
  the service runs before it keeps a record (`Directory.CheckRecord`)
  and the recovery record of a given recovery key
  (`Directory.RecoveryFor`), a group a device joins checked leaf by leaf
  against the directory and refused when Latere's member signed its
  group info or, joining on its own, when it is of an epoch before the
  one the approving device handed over (`Policy.CheckJoin`,
  `Policy.JoinGroup`, `Policy.JoinExternal`), a message padded to a
  fixed set of sizes (`Payload`), a message that verifies outside its
  group (`Entry`), history sealed for someone let in and announced in
  the commit that lets them in (`SealBundle`, `NewCommitNotice`), and
  the recovery key and the backup key it unlocks (`RecoveryKey`,
  `BackupKey`), which reaches a new device signed by the device that
  approved it. A message carries any file: `SealAttachment` encrypts it
  on the device under a key of its own, as a stream of 64 KiB chunks
  each sealed with AES-256-GCM, for upload to Storage, and
  `OpenAttachment` and `OpenAttachmentChunk` decrypt it; history bundles
  are sealed as the same stream. A file is at most `MaxAttachmentBytes`,
  2 GiB. It builds for WebAssembly.
- Package `memory`: what Latere remembers about a person across
  conversations, one Markdown file per memory in the person's Storage,
  for You or one of their projects. `ListMemories` answers the settings,
  every memory without its body, the projects' names, the bounds a memory
  is held to (`Limits`) and the words of an import (`ImportPrompt`).
  `GetMemory`, `UpdateMemory` and `DeleteMemory` address one memory by a
  `Ref`, which `Memory.Ref` returns; a change or a delete under `IfMatch`
  is refused with `ErrChanged` when the memory changed since it was read,
  and `UpdateMemory` with `Scope` and `Project` moves a memory between You
  and a project. `AddMemory` saves what the person typed, `ForgetEverything`
  deletes every memory, `UpdateSettings` turns Remember new things and
  Include sensitive topics on or off, `ListForgotten` and `RestoreMemory`
  bring back what a conversation forgot, `ExportMemory` streams a zip of
  every memory, ending its read with `ErrExportIncomplete` when the zip
  stops before its end, and `ImportMemory` saves what another assistant
  remembers. Each refusal code a program branches on has an error to test
  with `errors.Is`, such as `ErrPersonalOnly` for an organization's token
  and `ErrAgentToken` for a conversation's agent's. `AddMemory`,
  `ImportMemory`, `RestoreMemory` and a move are sent once.
- Package `attachment`: the file a message carries, the one type a
  session's messages and a conversation between people share, with its
  name, media type, size, digest, where it is kept, and a preview
  (`Preview`: a thumbnail, poster frame, first page or waveform).

### Changed

- **Breaking:** Package `agents`: `Attachment` is built on
  `attachment.Attachment`, which holds its `Name`, `MediaType`, `Size`,
  `Blob` and `Ref`, and adds `Preview`. Reading those fields is
  unchanged, and so is the JSON; a composite literal names them inside
  `Attachment: attachment.Attachment{...}`.

## [0.14.0] - 2026-10-29

### Changed

- **Breaking:** Package `agents`: a subagent's events and deltas are
  named by `Event.Subagent` and `Delta.Subagent`, read from the JSON
  field `subagent`, the name the agents API now gives the subagent whose
  conversation they belong to. `Event.Thread` and `Delta.Thread`, and
  the JSON field `thread`, are gone; the API's `thread.started`,
  `thread.message` and `thread.ended` events are now `subagent.started`,
  `subagent.message` and `subagent.ended`. Against an agents API that
  still sends `thread`, both fields read empty.

## [0.13.0] - 2026-10-25

### Added

- Package `account`: the provider that serves each model of an Effort
  level. `EffortModel.Provider` is the name a person reads for it, such
  as `"Anthropic"` or `"OpenRouter"`, which tells apart two models of one
  vendor served two ways; empty where the catalog names none.
- Package `agents`: a routine on dates. `StartsOnDates` runs the
  routine once at each of its `Dates`, times a schedule cannot say, from
  one to `MaxRoutineDates`; the API deletes the routine after the last.
  `CreateRoutine` and `ChangeRoutine` refuse with
  `ErrRoutineDatesInvalid` a list with no date, more than the most, the
  same time twice or nothing still to come; a date that has passed may
  stay beside one to come, so a change can write back what it read.
  `DatesZone` keeps the zone the person picked the dates in, written as
  the trigger's `timeZone`, so a routine read back says the dates as they
  picked them. `Routine.NextRunAt` is the first date still to come, and
  `ListWatches` reads the routine as one, with `Watch.Dates` and
  `Watch.DatesZone`. `TriggerSpec.Times` carries the list on any
  trigger.
- Package `agents`: the model a routine's runs start on. `Model` on
  `CreateRoutineParams` and `ChangeRoutineParams`, such as
  `"tier/thorough"`, is written as the trigger's `session.model`; `""` on
  a change returns runs to the agent's own model. `Routine.Model` and
  `Watch.Model` read it back, and `TriggerSession.Model` carries it on
  any trigger.
- Package `agents`: whether a routine's runs bring every result to the
  person. `Notify` on `CreateRoutineParams` and `ChangeRoutineParams`
  writes the trigger's `session.notify`; `NotifyJudged` tells the person
  only what the agent judges needs them, and `ErrRoutineNotifyInvalid`
  refuses any other value. `Routine.Notify` reads it back,
  `NotifyAlways` for a routine that names none. A routine that names
  none still writes none.
- Package `agents/routine`: `OnDates`, `MaxDates`, `Fields.Dates`,
  `Fields.DatesZone`, `Fields.Model` and `Fields.Notify`, with
  `CheckDates`, `CheckNotify`, `ErrDatesInvalid` and `ErrNotifyInvalid`.
  A routine on dates is written as `spec.times`, even for one date.
  `Manifest` takes `times` off a routine that comes to start another way,
  and `schedule`, `on` and `at` off one that comes to start on dates.

## [0.12.0] - 2026-10-25

### Added

- Package `account`: `GetForecast`, what the person will have spent by
  the end of the calendar month in `GetForecastParams.TimeZone`, as a
  `Forecast`: its `Days`, each `ForecastDay` spent so far and expected in
  both halves; `PersonLedPerDayMicroUSD`, `LastMonthMicroUSD`,
  `UnlistedProactiveMicroUSD`, the share's standing, `RunsOutOn`,
  `ProactiveSkipsFrom` and `ProactiveAgainAt`; and one `ForecastItem` per
  trigger, with its `Half` (`HalfPersonLed`, `HalfProactive`),
  `CountedIn`, `FiresOn`, its month so far and to come, `ExpectedAt` or
  `PerDay`, its skips and `Skipping` (`SkipAllowance`, `SkipShare`), and
  `InProactiveHalf`, which says which items add up to the proactive half.

### Changed

- Package `account`: `Spend` and `SpendDay` are now held to the account
  document, which describes the spending read.

## [0.11.0] - 2026-10-24

### Added

- Package `agents`: a trigger's weekly allowance. `TriggerSpec.Budget`, a
  `TriggerAllowance` in millionths of a US dollar, is the most the
  trigger's looks may cost in a rolling 7 days; a firing while the week's
  spend is at it starts nothing and records `FiringSkippedBudget`,
  counted as `FiringCounts.SkippedBudget`. `TriggerStatus.Spend`, a
  `TriggerSpend`, is the week as of the latest firing: the spend, the
  allowance, a look's mean cost, the looks expected and the projection,
  with `AheadOfPace` and `AtAllowance`. `SetTriggerAllowance` sets or
  takes off the allowance of any trigger, keeping every other field.
- Package `agents`: routines carry the allowance.
  `CreateRoutineParams.PerWeekMicroUSD` sets it,
  `ChangeRoutineParams.PerWeekMicroUSD` changes it or, at zero, takes it
  off, and a change that names none keeps it. `Routine.PerWeekMicroUSD`,
  `Routine.Spend` and `Routine.SkippedBudget` read it back, and
  `ErrAllowanceInvalid` matches an allowance below zero.
- Package `agents`: `Watch.CountsToward`, `Watch.PerWeekMicroUSD`,
  `Watch.Spend` and `Watch.SkippedBudget`. A wake carries the allowance
  and week of the trigger that started its session, which `ListWatches`
  reads from the page or, once per page, from the API.
- Package `agents`: `UserMessage.Spend`, the `FiringSpend` a woken turn's
  message carries, and `ToolUse.Attended` and
  `ApprovalRequested.Attended`, whether a person began the turn.
- Package `agents`: `Offer.PerWeekUSD` and `Offer.PerWeekMicroUSD`, the
  weekly allowance a held `trigger` call asks for, as the agent wrote it
  and in millionths of a dollar.
- Package `agents`: `Session.ContextReport` and `EventModelRequest` with
  its `ModelRequest` payload, the request's model, `Usage` and cost, whose
  `Context` is a `ContextReport`: the model's window, the tokens used and
  their `ContextSource` (`ContextFromProvider` or `ContextEstimated`), and
  the `ReportPart`s that fill it by `ReportPartKind`
  (`ContextInstructions` through `ContextFree`), with `UsedFraction` and
  `PartsInOrder`.
- Package `agents`: `TriggerStatus.Paused`, a `TriggerPause` saying
  why, by whom and when a paused trigger was paused, with `ByAgent` for a
  pause an agent's trigger tool made, and `Watch.Pause`, the same on a
  watch; and `FiringCounts.SkippedEnded`, the firings whose target
  session had ended.
- Package `account`: `GetSpend`, the person's spending by day as a
  `Spend`: each `SpendDay`'s `PersonLedMicroUSD` and `ProactiveMicroUSD`,
  the share of their credit for work that runs with nobody watching
  (`ShareBasisPoints`, `ShareMovedAt`), its `PoolMicroUSD`, what it spent
  in the last 7 days and `ProactiveLeftMicroUSD`; `MaxSpendDays` bounds
  the days read. The personal context alone.
- Package `agents/routine`: `Fields.PerWeekMicroUSD` and
  `Trigger.PerWeekMicroUSD`, written as `spec.budget.perWeek` by
  `Manifest`, which keeps the stored allowance when the fields carry
  none; `WriteAllowance`, `CheckAllowance` and `ErrAllowanceInvalid`.

## [0.10.0] - 2026-10-23

### Added

- Package `agents`: `ListWatches` and `ListWatchesPage`, a person's
  standing work of one agent in one reading, as `Watch`es: the routines
  they made, the routines and event triggers an agent offered and they
  accepted, and the wakes an agent set to look again in a session, each
  with its `WatchKind`, when it looks next, the sentence of why it exists,
  who set it (`SetBy`), the session it came from, whether it is paused,
  and a wake's `Count`. An offer's why is read from the session's log it
  was offered in; pass the whys of the last reading in
  `ListWatchesParams.KnownWhys` and a reading reads no log again.
- Package `agents`: `SuspendTrigger`, a pause or a resume of any trigger
  by its id or name that keeps every other field as the API answered it,
  for a watch that is no routine, such as an event trigger an agent
  offered.
- Package `agents`: `GetTriggerHold` and `SetTriggerHold`, the hold of
  every trigger of the caller at once, as a `TriggerHold`. Each trigger
  keeps its own pause, so releasing the hold brings back only those that
  were on.
- Package `agents`: `TriggerStatus.Origin` and `Routine.Origin`, the
  `TriggerOrigin` of a trigger an agent's call made: an offer the person
  accepted, with who accepted it, or a wake.
- Package `agents`: `TriggerSpec.At`, a trigger that fires once;
  `TriggerSession.Target`, the session every firing continues;
  `TriggerSession.Attended`, sessions whose questions wait for the
  trigger's owner; `TriggerSession.Notify`, whether every turn brings its
  result (`NotifyAlways`) or the agent judges (`NotifyJudged`); and the
  firing outcome `FiringSkippedEnded`.
- Package `agents`: `Session.Wake`, a session's `PendingWake`;
  `Session.Outcome` and `SessionStatus.Outcome`, whether a turn a trigger
  or a wake began was `TurnQuiet` or `TurnNotified`; `Session.Notice`,
  what the agent last brought to the person; and `UserMessage.FiringID`
  and `UserMessage.Wake`, the firing and the wake a message came from.
- Package `agents/routine`: the shape of the trigger of an agent's offer
  on events no routine starts from, such as a repository's pushes, for a
  server that applies it and a client that reads it: `OfferKey`, the key
  an offer's call names its trigger by; `EventPrefix`, `EventName` and
  `IsEventName`, its name; `StartsOn`, whether a routine starts on given
  events; `EventSuffix` and `EventMessage`, its message; and `ReadEvent`
  with `EventFields`, its title and ask read back.
- Package `agents`: `ToolWake` and `ToolNotify`, the names of the tools
  an agent sets its next wake and brings a turn's result to the person
  with.

### Changed

- **Breaking:** Package `agents`: `ToolUse.Offer` reads every held call of
  the `trigger` tool, a proposal of a routine or of a trigger that fires
  on events (`Offer.On`), a change, a resume and a delete (each naming
  its `Offer.TriggerID`), and the held call of the `wake` tool past its
  row of looks (`Offer.At`, `Offer.After`, `Offer.Note`). Before, it read
  a proposal alone and returned an error for the others; check
  `Offer.Action` before wording an offer as a new routine.
- Package `agents`: `ApplyTrigger`, `GetTrigger` and `DeleteTrigger`
  refuse the name `hold` before any request, since its path is the
  hold's.

## [0.9.1] - 2026-10-23

### Changed

- Package `agents`: a `FileReader` from `ReadBlob` reports a kept file's
  own media type in `MediaType` and its length in `Size`, the ones its
  `Attachment` or `KeptFile` records, before any byte is read, as the
  Agents API answers them from Topos v0.53.0 on; before, a file that is no
  picture read as `application/octet-stream`, and its size was -1 for a
  session that keeps no folder. A file that is no picture is still a
  download, an HTML page and an SVG included. Nothing in the SDK's code
  changes; the docs and a test state the answer.

## [0.9.0] - 2026-10-23

### Added

- Package `agents`: `PageSandbox` and `PagePolicy`, the frame sandbox and
  the content security policy a program shows a page an agent made under,
  the same rules Latere Chat and Latere Studio follow, and the package
  docs on how to apply them.

## [0.8.0] - 2026-10-22

### Added

- Package `signin`: `UserInfo.OrgID`, the organization a sign-in acts in,
  and `UserInfo.Handle`, the handle the person claimed.
- Package `signin`: `ListOrganizations`, the organizations a person
  belongs to as `Organization`s with their slug, owner, the time they
  joined and the identity provider that manages the membership, and
  `GetHandle`, the handle a person claimed, by their id, with
  `ErrNoPerson` for an id that names no one. Both read the sign-in
  service's own records with the sign-in's access token, need no client
  id, and never follow a redirect.
- Package `signin`: `Error.Description` holds the message of a refusal the
  sign-in service answers as `{"error", "message"}`, as its own records
  do.
- Package `agents`: `SendMessageParams.Askable`, a message whose program
  shows the person the ask the platform may hold it for, and
  `SendMessageParams.Answers`, the ask an earlier copy of the message was
  refused with, which this copy answers, at most `MaxAnswersBytes`.
- Package `agents`: `NewAttachment.Blob`, a file a message of the session
  already carried, kept by its digest without its bytes being sent again.
- Package `agents`: `ForkSessionParams.AtSeq`, a fork at the end of a
  given turn; `Untitled`, a fork with no title where an empty `Title`
  gives it the parent's; `NewTree`, a fork that starts a conversation of
  its own; and `Attachments`, the files of the fork's first message.
- Package `web`: `Icon.CacheControl` and `Icon.ETag`, the answer's cache
  policy and entity tag, for a server that passes the icon on.
- Package `signin`: a person's keys, `CreateKey`, which answers the
  `Key` with its value once, takes a sign-in through a first-party client
  and is sent once, `ListKeys`, every key or those of one organization or
  of the personal account, and `RevokeKey`, with `ErrNoKey` for an id that
  names no key of the person, and the `KeyStatus` of a key. Each takes the
  sign-in's access token and never follows a redirect.
- Package `signin`: `Error.Detail`, the developer detail the sign-in
  service's own records answer beside the message.
- Package `signin`: `SwitchOrganization`, a refresh that moves the
  sign-in to an organization the person belongs to, or to their personal
  account.
- Packages `evals`, `apps` and `llm`: `BasePath`, the path the API serves
  the capability under, for a program that takes a capability's address
  as one URL with that path and cuts it off to name the base URL.

## [0.7.0] - 2026-10-22

### Added

- Package `account`: `DeviceNetwork.Blocked`, the hosts a computer on the
  `open` network blocks, and the `open` mode in `DeviceNetwork.Mode`'s
  docs.
- Package `latere`: `WithoutCredentials` builds a `Config` that sends no
  credential on any request, for a program that holds none, such as a
  server that shows a shared conversation to a visitor. The API's public
  reads answer it; a request that sends the key is refused before it is
  sent.
- Package `agents`: `GetSessionSummary` counts the sessions a list would
  page through, by running, waiting for approval, idle and ended.
- Package `agents`: the read of a shared conversation by its link's token:
  `GetSharedPage`, a page of the events a reader sees with the `Share`'s
  title and bound, `ListSharedEvents`, every event, and `ReadSharedBlob`,
  a file it kept as a `FileReader`.
- Package `agents`: `FileReader.CacheControl`, `ETag` and
  `ContentDisposition`, the answer's headers a program that passes a
  file on to a browser sends with it.
- Package `account`: links that share a conversation, `CreateShare`,
  `ListShares` and `ListSharesPage`, `UpdateShare`, which moves a link to
  the last finished turn, and `DeleteShare`, with the `Share` model and its
  audience, state and reason types. `CreateShare` is sent once.
- Package `account`: `StartCheckout`, a payment page for credit in the
  wallet of the token's context, sent once.
- Package `account`: `GetDataPlaces`, where the installation keeps
  sessions and runs their machines, and `GetModelDataTerms`, the data
  terms, display name and price of up to `MaxDataTermsModels` models.
- Package `account`: `UpdatePreferences`, a merge patch of the
  preferences, and the writes made one at a time: `AddSite` and
  `RemoveSite`, `BookmarkTurn` and `UnbookmarkTurn`, and `PinApp`,
  `UnpinApp`, `PinProject` and `UnpinProject`. `Network` and `Bookmarks`
  keep their JSON behind `RawJSON`.
- Package `account`: `RecordArrival`, the site that referred a person and
  the page they landed on.
- Package `account`: projects, which group a context's conversations and
  hold the apps they work on: `ListProjects`, `ListProjectsPage`,
  `CreateProject`, `GetProject`, `UpdateProject`, `DeleteProject`,
  `AddProjectApp` and `RemoveProjectApp`, with `ErrProjectLimit` and
  `ErrAppNotInContext`. `CreateProject` is sent once.
- Package `account`: a routine's ways in from outside. `ListTriggerInbound`
  lists the links and email addresses the caller's triggers have, and
  whether email is on; `IssueTriggerHook` and `RemoveTriggerHook` give a
  trigger a link another app posts to, and `IssueTriggerAddress` and
  `RemoveTriggerAddress` an email address, with `ErrMailNotEnabled`.
- Package `apps`: `ListAppSessions` and `ListAppSessionsPage`, the
  sessions whose publishes built an app, and `GetAppVisits`, its daily page
  loads and visitors with their totals.
- Package `apps`: `GetDeployByID`, a deploy by its ID alone, and
  `GetDeployApp`, the app a deploy belongs to as an `OpenApp`.
- Package `apps`: `ReadThumbnail`, a deploy's picture as a
  `ThumbnailReader` with its media type, size, ETag and Cache-Control, at
  `ThumbnailLarge` or `ThumbnailSmall`, with `Thumbnail.Deploy` and
  `ErrNoThumbnail`.
- Package `apps`: `CreateFramePass`, the address that shows a private app
  in a frame of another site, with `ErrFrameUnavailable`, and
  `EndGateSessions`, which ends a person's sign-ins at private apps as
  they sign out.
- Package `apps`: `GetListing`, `RequestListing` and `WithdrawListing`, an
  app's place in the gallery.
- Package `apps`: `GetApp`, `GetDeployApp` and `ReadThumbnail` read a
  public or unlisted app as anyone does with a `Config` built
  `WithoutCredentials`.
- Package `storage`: `MoveFile` and `MoveFolder`, a rename of a file, or
  of a folder with everything under it in one change, each sent once;
  `DeleteFile` and `DeleteFolder`, to the trash or with
  `DeleteOptions.Permanent` for good; and `ListSharedWithMe` and
  `ListSharedWithMePage`, the shares other people gave you, as `Grant`s
  with their `Permission`, `GranteeKind` and `GrantStatus`.
- Package `evals`, new: reads Evals, the platform's experiments on agents
  and programs. The five definitions, `Metric`, `Grader`, `Task`, `Suite`
  and `Candidate`, are each listed, read by a ref, a name, name@digest, a
  digest or an id, and listed by version; experiments are listed and read
  with their cells and comparisons, and `StreamEvents` follows one to its
  end, connecting again after a drop, its last event an `EventEnd` with
  the state the experiment finished in.
- Package `web`: `ReadImage` and `ReadSharedImage`, a picture of the web
  an answer shows, read through the platform as the person or as a share
  link's reader, at `ImageSmall` or `ImageLarge`, as an `ImageReader` that
  streams it with its media type, length and Cache-Control. An address
  the platform shows no picture for returns an error matching
  `ErrNoImage`.
- Package `agents`: `ToolUse.Offer` reads an agent's offer of a routine,
  a held call of its `trigger` tool (`ToolTrigger`): the routine's name,
  what each run asks, the agent's sentence of why, and its schedule read
  into a `RoutineSchedule`. Answer it with `ConfirmTool`; only the person
  who started the session may.

## [0.6.0] - 2026-10-22

### Added

- Package `agents/routine`: a routine's trigger shape alone, importing the
  standard library alone, for code that writes or reads a routine without
  calling the API, such as a server that applies a routine an agent
  offered once the person accepted it. `Manifest` writes the trigger,
  `Name` makes its name from a key, `Read` reads a trigger back, `New`
  and the `Check` functions hold the bounds, and `Cron` and
  `ParseSchedule` write and read the schedule. `agents` writes and reads
  routines through it, so the shape is defined once.
- Package `web`: `GetIcon` reads a site's icon through the platform, a
  PNG of `IconSmall`, 32 pixels a side, or `IconLarge`, 64, to draw beside
  a link to the site, as Latere Chat does. A site without an icon answers
  nil and no error; a host the platform reads no icon for, such as
  localhost or an IP address, returns an error matching `ErrInvalidHost`,
  and an answer past `MaxIconBytes`, 64 KiB, one matching
  `ErrIconTooLarge`. The request carries no credential, since the route
  reads none.
- Package `latere`: `Request.Anonymous` sends a request without the
  `Config`'s credential, for a route that reads none: no token is fetched
  for it, and a 401 is not answered by renewing one.

### Changed

- Package `agents`: `RoutineSchedule` and `RoutineStart` are aliases of
  `routine.Schedule` and `routine.Start`, and the routine errors are
  `routine`'s, so their text starts `routine:`. Matching them with
  `errors.Is` is unchanged.

## [0.5.0] - 2026-10-21

### Added

- Package `agents`: `ToolResult.Meta`, a `*ToolMeta` with what a tool
  call recorded beside its result: the file a read, a write or an edit
  touched, with the `Size` a write or an edit left, and a publish's
  `PublishRecord` of the app, its address, the preview, the deploy, the
  `PublishStatus` and the visibility. `ToolMeta.RawJSON` keeps the rest,
  such as a todo call's list.
- Package `agents`: `EventSessionMachine`, whose payload `SessionMachine`
  names the machine a session's commands run on from that event on, an
  `AttachedMachine` with its working directory, and the `MachineReason`
  the session has it for.
- Package `agents`: `Session.Folder`, the session's folder of files in
  Storage by its owner and path, so a kept file's `Ref` names a file
  package `storage` reads, in the person's space or an organization's.

## [0.4.0] - 2026-10-21

### Added

- Package `agents`: routines, an instruction an agent carries out again
  and again on a schedule or when something arrives from a link or an
  email, written as a trigger in the one shape Latere Chat and Latere
  Studio both read. `CreateRoutine`, `ListRoutines`, `GetRoutine`,
  `ChangeRoutine`, `DeleteRoutine` and `RunRoutine` make, read, change,
  delete and run them; `RoutineOf` reads a trigger as one and
  `RoutineName` names the trigger a create's key makes, so the same key
  never makes a second routine. A change keeps every field of the trigger
  it does not name, those this version does not model included. The
  name, the instruction and the schedule are checked before anything is
  sent, each refusal a sentinel error such as `ErrRoutineTitleMissing`;
  a name that names no routine matches `ErrNotRoutine` and
  `latere.ErrNotFound`. `RunRoutine` is sent once, as `FireTrigger` is.
- Package `agents`: `SearchSessions` and `SearchSessionsPage` search what
  was said in the sessions the list would list, each `SessionMatch` with
  its newest matching events and an excerpt whose matched words are
  marked, within `MaxSearchQuery` and `MaxSearchLimit`.

### Fixed

- Package `latere`: `Config.Stream` follows a quiet stream for as long as
  the server serves it. A connection that delivered only keepalive
  comments, as a stream with no new events does until the API closes it
  when its token expires, counted against the retry limit, so such a
  stream ended with a "dropped" error after the configured retries, about
  fifteen minutes with the default retries and the API's five-minute
  tokens. Now any frame or comment the server sends resets the count, and
  only connections on which the server sent nothing count toward the
  limit.

## [0.3.0] - 2026-10-21

### Added

- Package `agents`: `ReadBlob` reads a file a session keeps by its
  digest, a message's file or a picture or file an answer showed or
  linked, also after the session ended; `ReadSessionFile` reads a file of
  the session's working directory as it is now, while its machine runs.
  Each returns a `*FileReader` with the file's media type and size, which
  the caller closes; the configured timeout bounds the wait for the
  answer, and only the context bounds the read after it. A read of a
  machine that is not running returns an error matching
  `ErrFileUnavailable`, and a file past `MaxSessionFileBytes`, 32 MiB, one
  matching `ErrFileTooLarge`; both still unwrap to the `*latere.Error`.
- Package `agents`: `EventFilesKept`, whose payload `FilesKept` names the
  pictures and files an answer showed or linked that the session kept,
  each a `KeptFile` with the digest `ReadBlob` takes, and those it did not
  keep, each with a `SkipReason`.
- Package `agents`: the labels the platform writes on a session's
  sandbox, `LabelSession`, `LabelAgent`, `LabelAgentVersion`,
  `LabelTrigger` and `LabelTriggerFiresOn`, and the annotation
  `AnnotationAgentName`, for a program that serves sessions on a person's
  computer.
- Package `account`: `RenameDevice` renames one of the person's computers,
  sent again after a failure since it only sets the name, with the bound
  `MaxDeviceName`. `RemoveDevice` removes one; removing a computer the
  person no longer holds succeeds, so a removal is safe to repeat.
- Package `account`: `GetEnvironmentsContext` reads the labels a create in
  Environments must carry in the token's context, and `LabelKind` with
  `KindDevice` is the label that marks a computer's environment.
- The core `latere`: `ReadUnverifiedClaims` reads the audiences, subject,
  expiry and organization of a token the platform issued without
  verifying it, for a program that is handed a token to present to the
  API.

## [0.2.0] - 2026-10-20

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
