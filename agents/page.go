// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

// PageSandbox is the sandbox a page an agent made runs in: an HTML file of
// a session, such as one the agent wrote or one an answer linked, shown as
// a page. Its value is the tokens of an iframe's sandbox attribute or of a
// Content-Security-Policy's sandbox directive: scripts, and nothing else.
// The package docs say how a program shows such a page.
//
// Without allow-same-origin the page's origin is opaque and matches no
// other, so its scripts reach no cookie, storage or page of the program
// that shows it, and no address with the person's credentials. Without
// allow-top-navigation, allow-popups, allow-forms, allow-modals and
// allow-downloads it cannot move the program's page, open a window, send a
// form, hold the page with a dialog or start a download.
const PageSandbox = "allow-scripts"

// PagePolicy is the Content-Security-Policy a page an agent made runs
// under, beside PageSandbox. A program puts it first in the page's head;
// the package docs say how. Directive by directive:
//
//   - default-src 'none': nothing loads that a directive below does not
//     name.
//   - script-src 'unsafe-inline' 'unsafe-eval' https: data: blob:: the
//     page's own scripts run, inline or from a public address, as a page an
//     agent writes usually carries them. They run in the page's opaque
//     origin, so what they run reaches nothing of the program's.
//   - style-src 'unsafe-inline' https: data:, font-src https: data:,
//     img-src https: data: blob: and media-src https: data: blob:: the page
//     looks as it was written, with its styles, fonts, pictures and sound.
//   - connect-src 'none': no script sends or fetches anything, by fetch,
//     XMLHttpRequest, WebSocket, EventSource or a beacon.
//   - frame-src 'none' and worker-src 'none': no frame inside it, and no
//     worker that would run outside these rules.
//   - form-action 'none': no form sends anything anywhere.
//   - base-uri 'none': no base element moves where the page's addresses
//     lead.
//
// A policy the page carries itself still holds beside this one, and can
// only narrow it.
const PagePolicy = "default-src 'none'; " +
	"script-src 'unsafe-inline' 'unsafe-eval' https: data: blob:; " +
	"style-src 'unsafe-inline' https: data:; " +
	"font-src https: data:; " +
	"img-src https: data: blob:; " +
	"media-src https: data: blob:; " +
	"connect-src 'none'; " +
	"frame-src 'none'; " +
	"worker-src 'none'; " +
	"form-action 'none'; " +
	"base-uri 'none'"
