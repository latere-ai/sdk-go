// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package routine

import (
	"regexp"
	"slices"
	"strings"
)

// An agent may offer the person standing work in a session, which is made,
// as theirs, once they accept. An offer on a schedule, on every email that
// arrives or on every call of the person's link is a routine, written by
// Manifest under Name(OfferKey(...)). An offer on any other events, such
// as a repository's pushes, is no routine a program could show or change
// as one, so it is written the same way under a name of its own,
// EventName(OfferKey(...)), with the events as its spec.on and EventMessage
// as its session's message, which hands each run the event that started
// it. ReadEvent reads such a trigger back.

// EventPrefix starts the name of the trigger of an offer on events no
// routine starts from; the 20 lowercase hexadecimal digits Name gives the
// same key follow it. No routine has such a name, so the routine methods
// never list, change or delete one as a routine.
const EventPrefix = "event-"

// EventSuffix follows the ask in the message of the trigger of an offer on
// events no routine starts from: the event that started the run, read
// through the trigger's event placeholders. It is for the model, so it is
// in English whatever the person's language; ReadEvent takes it off.
const EventSuffix = "\n\nThe event that started this run:\nProduct: {{event.product}}\nVerb: {{event.verb}}\nResource: {{event.resource}}\nActor: {{event.actor}}\nTime: {{event.time}}\n\n{{event.payload}}"

// eventNamePattern is the shape of the name of an offer's trigger on
// events.
var eventNamePattern = regexp.MustCompile(`^` + EventPrefix + `[0-9a-f]{20}$`)

// OfferKey returns the key the trigger an agent's offer makes is named by:
// the session's id, "/" and the id of the call that offered it. The same
// call always names the same trigger, so applying it again after a lost
// answer makes no second one.
func OfferKey(sessionID, toolUseID string) string { return sessionID + "/" + toolUseID }

// EventName returns the name of the trigger of an offer on events no
// routine starts from, made with key: EventPrefix and the 20 hexadecimal
// digits Name(key) has.
func EventName(key string) string { return EventPrefix + strings.TrimPrefix(Name(key), Prefix) }

// IsEventName reports whether name has the shape of the name of an offer's
// trigger on events no routine starts from.
func IsEventName(name string) bool { return eventNamePattern.MatchString(name) }

// StartsOn returns how a routine starts on the events of product with
// verbs, and false when no routine starts on them: FromLink for every call
// of the person's link and FromEmail for every email that arrives, each
// the one verb "received" and narrowed by no resource or payload
// condition. An offer on events StartsOn refuses is written under
// EventName.
func StartsOn(product string, verbs []string, narrowed bool) (Start, bool) {
	if narrowed || !slices.Equal(verbs, []string{verbReceived}) {
		return "", false
	}
	switch product {
	case productWebhook:
		return FromLink, true
	case productMail:
		return FromEmail, true
	}
	return "", false
}

// EventMessage returns the message of the trigger of an offer on events no
// routine starts from: the ask written as literal text, so a {{ in it is
// never read as a placeholder, and EventSuffix after it.
func EventMessage(ask string) string { return escapeTemplate(ask) + EventSuffix }

// EventFields are what the trigger of an offer on events no routine starts
// from holds of what the agent wrote.
type EventFields struct {
	// Title is the trigger's name, as a person reads it, and the title of
	// each session a run starts.
	Title string
	// Ask is what each run asks, as the agent wrote it.
	Ask string
	// Paused says the trigger runs nothing until it is resumed.
	Paused bool
}

// ReadEvent reads t as the trigger of an offer of agent's on events no
// routine starts from, and reports false when it is none: a trigger
// without the Label of agent, whose name is not EventName's shape, or that
// fires on no events. The title and the ask are read as the agent wrote
// them, the ask without EventSuffix.
func ReadEvent(t Trigger, agent string) (EventFields, bool) {
	if agent == "" || t.Labels[Label] != agent || !IsEventName(t.Name) || t.Product == "" {
		return EventFields{}, false
	}
	title := t.DisplayName
	if title == "" {
		title = unescapeTemplate(t.Title)
	}
	ask := unescapeTemplate(strings.TrimSuffix(t.Message, EventSuffix))
	return EventFields{Title: title, Ask: ask, Paused: t.Suspend}, true
}
