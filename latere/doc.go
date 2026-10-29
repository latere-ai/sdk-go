// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package latere holds what every package of the Latere Go SDK shares: the
// configuration a client is built from, credentials, the error type,
// pagination and the HTTP transport that sends and retries requests.
//
// The SDK has one package per capability of the Latere API. Import the ones
// you use:
//
//	latere.ai/x/sdk-go/agents       sessions with agents, their events and files, and triggers
//	latere.ai/x/sdk-go/account      your Effort levels, wallet, preferences and computers
//	latere.ai/x/sdk-go/memory       what Latere remembers about a person across conversations
//	latere.ai/x/sdk-go/apps         apps, their deploys, build logs and releases
//	latere.ai/x/sdk-go/connections  the services a person connects for agents to use
//	latere.ai/x/sdk-go/storage      files in Storage: upload, read, list, link
//	latere.ai/x/sdk-go/llm          calling models in the Lux dialect
//	latere.ai/x/sdk-go/llm/wire     the Lux dialect's types alone, sending nothing
//	latere.ai/x/sdk-go/web          a site's icon, read through the platform
//	latere.ai/x/sdk-go/signin       signing a person in, for apps Latere registers
//
// Build one [Config] and hand it to the New function of each capability:
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	ag := agents.New(cfg)
//	wallet, err := account.New(cfg).GetWallet(ctx)
//
// # Credentials
//
// Every request carries a bearer token from a [TokenSource]. The simplest is
// a key created under Keys in the console, given with [WithKey] or in the
// LATERE_KEY environment variable: the SDK exchanges it for a token of a few
// minutes and renews the token before it expires ([NewKeyTokens]). A
// program that acts for a person who signed in, a desktop app for instance,
// passes the person's sign-in to [NewActorTokens] instead, which exchanges
// it for tokens addressed to the API; package signin obtains and refreshes
// that sign-in.
//
// A route that reads no credential, such as a site's icon, is called with
// [Request.Anonymous] set: no token is fetched for it, and none is sent. A
// program that holds no credential at all, such as a server that shows a
// shared conversation to a visitor, builds its Config with
// [WithoutCredentials], which sends none on any request; the API's public
// reads answer it, and every other call is refused with 401.
//
// [ReadUnverifiedClaims] reads what a token the platform issued says of
// itself, whom it names, its context and its expiry, without verifying
// any of it, for a program that is handed a token to present to the API.
// The API verifies a token; reading its claims does not.
//
// # Errors
//
// A call the API refused returns an [*Error] with the HTTP status, the API's
// error code, its message and the request's id. Test for the common cases
// with [errors.Is]:
//
//	if errors.Is(err, latere.ErrNotFound) {
//		// the session does not exist, or you may not read it
//	}
//
// Use [errors.As] to read the code and the details:
//
//	var apiErr *latere.Error
//	if errors.As(err, &apiErr) {
//		log.Printf("%s (request %s)", apiErr.Code, apiErr.RequestID)
//	}
//
// An answer without the API's error envelope, such as a proxy's page, has
// the HTTP status text as its Message; [Error.Body] holds the response as
// it was sent.
//
// # Retries
//
// A request that failed on the network, or that the API answered with 408,
// 429, 500, 502, 503 or 504, is sent again with exponential backoff and
// jitter, honoring Retry-After, when sending it twice is safe, as its
// [RetryRule] says: by default a GET, HEAD, PUT or DELETE request, and a
// POST that carries an idempotency key, which the API reads on POST alone.
// The capability packages send a key on the POSTs that create or send
// something, and retry every PATCH that only sets values,
// llm.CountTokens, which changes nothing, and the POSTs whose repeat
// leaves what one would: account.IssueTriggerHook and
// account.IssueTriggerAddress, whose repeat replaces the link a lost
// answer issued, and apps.CreateFramePass, whose unused pass lapses.
// These calls are sent once instead, never again after a failure:
//
//	account.CreateShare               the account API takes no key on it
//	account.CreateProject             the account API takes no key on it
//	account.StartCheckout             a repeat opens a second payment page
//	agents.FireTrigger                the API forgets a firing's key on a server error
//	agents.RunRoutine                 a routine's run is a firing of its trigger
//	apps.CreateApp, apps.Invite       the apps API takes no key yet
//	connections.ConfirmConnection     a finished sign-in answers 410 to a repeat
//	connections.ReconnectConnection   a repeat replaces the sign-in it started
//	storage.StartUpload               the storage API takes no key; an open upload lapses
//	storage.MoveFile, MoveFolder      a repeat finds nothing left to move
//	llm.Generate, llm.Stream          the model may have run and been billed
//	memory.AddMemory                  the account API takes no key on it
//	memory.ImportMemory               the account API takes no key on it
//	memory.RestoreMemory              a repeat finds nothing left to restore
//	memory.UpdateMemory, a move       a repeat finds nothing left to move
//
// storage.CompleteUpload is sent again after an answer of a server error,
// which leaves the upload open, but never after a failure on the network,
// after which a completed upload is gone ([RetryAnswers]).
//
// [WithMaxRetries] sets how many times; zero turns retries off. A 401 is
// retried once with a new token when the token source implements
// [Resetter], as the SDK's own sources do.
//
// A call whose context has no deadline is bounded by [DefaultTimeout], or
// the bound [WithTimeout] gives; a call cut off returns an error matching
// [context.DeadlineExceeded]. A request that sends a file's bytes bounds
// each attempt instead, by the timeout and [TransferTime] for their
// length, and sends an attempt cut off again.
//
// A signed link, such as an upload part's, carries its credential in its
// query; an error names it without the query.
//
// # Pagination
//
// A list method returns an [iter.Seq2] that fetches pages as the loop needs
// them:
//
//	for s, err := range ag.ListSessions(ctx, nil) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(s.ID, s.Title)
//	}
//
// Each list method has a Page variant that reads one [Page] and returns its
// cursor, for a program that pages on its own.
//
// # Observability
//
// The SDK depends on the standard library alone. Pass an [*http.Client] whose
// transport traces or measures requests with [WithHTTPClient], or receive a
// call before and after every attempt with [WithHooks].
package latere
