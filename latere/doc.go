// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package latere holds what every package of the Latere Go SDK shares: the
// configuration a client is built from, credentials, the error type,
// pagination and the HTTP transport that sends and retries requests.
//
// The SDK has one package per capability of the Latere API. Import the ones
// you use:
//
//	latere.ai/x/sdk-go/agents   sessions with agents, and the events of a session
//	latere.ai/x/sdk-go/account  your Effort levels, wallet, preferences and computers
//	latere.ai/x/sdk-go/signin   signing a person in, for apps Latere registers
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
// # Retries
//
// A request that failed on the network, or that the API answered with 408,
// 429, 500, 502, 503 or 504, is sent again with exponential backoff and
// jitter, honoring Retry-After, when sending it twice is safe: GET, HEAD,
// PUT and DELETE, and POST or PATCH requests that carry an idempotency key.
// The capability packages send an idempotency key on every request that
// creates something, so those are retried too. [WithMaxRetries] sets how many
// times; zero turns retries off. A 401 is retried once with a new token when
// the token source implements [Resetter], as the SDK's own sources do.
//
// A call whose context has no deadline is bounded by [DefaultTimeout], or
// the bound [WithTimeout] gives; a call cut off returns an error matching
// [context.DeadlineExceeded].
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
