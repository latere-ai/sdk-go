// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"
	"time"

	"latere.ai/x/sdk-go/agents/routine"
	"latere.ai/x/sdk-go/latere"
)

// A watch is standing work an agent does for a person with nobody
// waiting: a routine, a trigger that fires on events which the agent
// offered in a session and the person accepted, or a wake the agent set
// to look again later in a session. Each is a trigger of the person's
// own; the watch methods read them in one shape, with the sentence of why
// each exists and the session it came from, so every program that shows a
// person what is watching for them shows the same.

// WatchKind is what kind of standing work a Watch is.
type WatchKind string

// The kinds of Watch.
const (
	// WatchRoutine is a routine: it runs on a schedule, each time
	// something arrives at its link or its address, or once, at a time the
	// person picked. The routine methods read and change it by the watch's
	// Name.
	WatchRoutine WatchKind = "routine"
	// WatchEvent is a trigger that fires on the events its On selects,
	// which an agent offered and the person accepted: events no routine
	// starts from, such as a repository's pushes. It carries the
	// RoutineLabel of its agent under a name of routine.EventName's shape,
	// which no routine has, so the routine methods leave it alone.
	WatchEvent WatchKind = "event"
	// WatchWake is a wake an agent set in a session: a trigger that fires
	// once, at NextAt, and sends the agent's note back to that session.
	// It ends when it fires, or with its session.
	WatchWake WatchKind = "wake"
)

// SetBy is who set a watch.
type SetBy string

// Who set a Watch.
const (
	// SetByPerson is the person, through a program or the API.
	SetByPerson SetBy = "person"
	// SetByOffer is the agent, which offered it in a session, and the
	// person, who accepted at the watch's CreatedAt.
	SetByOffer SetBy = "offer"
	// SetByAgent is the agent alone: a wake it set.
	SetByAgent SetBy = "agent"
)

// A Watch is one piece of standing work as Client.ListWatches reads it
// from its trigger. Pause or resume any watch with Client.SuspendTrigger
// and delete it with Client.DeleteTrigger, by its TriggerID.
type Watch struct {
	// Kind is what kind of watch it is.
	Kind WatchKind
	// TriggerID is the trigger's id, which each session it starts names as
	// its TriggerID.
	TriggerID string
	// Name is the trigger's name, which the routine methods take for a
	// routine.
	Name string
	// Title is its name as a person reads it: a routine's title, the name
	// an agent offered a trigger under, or the line it wrote for a wake.
	Title string
	// Why is the sentence that says why it exists: what the agent wrote
	// when it offered it, for one set by an offer; the routine's
	// instruction, for one the person made; and a wake's Title. For an
	// offer whose call its session's log no longer holds, it is what each
	// run asks: the routine's instruction, or the event trigger's ask.
	Why string
	// SetBy is who set it.
	SetBy SetBy
	// Origin is the call that made it, for one set by an offer or a wake;
	// nil for one the person made.
	Origin *TriggerOrigin
	// Session is the session it came from, for one set by an offer or a
	// wake: where the agent offered it, or the session a wake looks again
	// in. Nil for one the person made.
	Session *WatchSession
	// Starts is how a routine starts; empty for the other kinds.
	Starts RoutineStart
	// Schedule is when a routine on a schedule runs; nil for the other
	// kinds, and for a schedule of a shape a routine does not write.
	Schedule *RoutineSchedule
	// At is when a routine that runs once runs, and AtZone the IANA time
	// zone the person picked it in, paused or not; the zero time and empty
	// for the other kinds.
	At     time.Time
	AtZone string
	// On selects the events an event watch fires on; nil for the other
	// kinds.
	On *EventFilter
	// NextAt is when it looks next: a routine's next run, the time of a
	// routine that runs once, or the time of a wake. Nil while it is
	// paused, and for one that looks when an event arrives.
	NextAt *time.Time
	// Notify is whether every run brings its result to the person or the
	// agent judges; a wake's runs are always judged.
	Notify NotifyPolicy
	// Model is the model each session it starts runs on, such as
	// "tier/thorough"; empty when they start on the agent's own model, and
	// for a wake, which continues its session on that session's model.
	Model string
	// Paused says it fires nothing until it is resumed. The hold of every
	// trigger, which Client.GetTriggerHold reads, leaves it as it is.
	Paused bool
	// Pause is why and by whom a paused watch was paused: the sentence an
	// agent gave when it paused the watch itself, with the session it did
	// so in, or the person who paused it. Nil while it runs, and for a
	// watch paused before the API began recording pauses.
	Pause *TriggerPause
	// Count is a wake's place in its session's row of wakes with no word
	// from the person between them, PendingWake.Count; zero for the other
	// kinds.
	Count int
	// LastRunAt is when it last started or continued a session, and
	// LastSessionID that session; nil and empty before its first run.
	LastRunAt     *time.Time
	LastSessionID string
	// CreatedAt is when it was set: for one set by an offer, when the
	// person accepted it.
	CreatedAt time.Time
	// CountsToward is the TriggerID of the trigger whose allowance the
	// watch's looks count toward: its own, for a routine and an event
	// watch, and for a wake, the trigger that started the wake's session.
	// Empty for a wake in a session a person started, which counts toward
	// no trigger; the session's own budget bounds it.
	CountsToward string
	// PerWeekMicroUSD is the weekly allowance of the trigger CountsToward
	// names, in millionths of a US dollar: the most the looks counted
	// toward it may cost in a rolling 7 days. Zero when it has none, and
	// for a wake whose trigger the caller can no longer read.
	PerWeekMicroUSD int64
	// Spend is the week of the trigger CountsToward names, as of that
	// trigger's latest firing: what its looks cost in the 7 days before,
	// and the pace they are on. Nil for a trigger that has not fired since
	// the API began writing it, and for a wake that counts toward none or
	// whose trigger the caller can no longer read.
	Spend *TriggerSpend
	// SkippedBudget counts the watch's own firings the allowance skipped,
	// which started nothing because the week's spend was at it. A wake
	// skipped this way is tried again each minute until the spend allows
	// it, or it is too late.
	SkippedBudget int64

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the watch's trigger, with
// every field, including those this package does not model; nil for a
// Watch the program built itself.
func (w *Watch) RawJSON() json.RawMessage { return w.raw }

// A WatchSession is the session a watch came from.
type WatchSession struct {
	// ID is the session's id.
	ID string
	// Title is its title as it is now; empty for an untitled session, and
	// for one the person can no longer read, such as one deleted since.
	Title string
}

// watchOf reads t as a watch of agent, and reports false when it is none:
// a wake, whose agent is read with its session; a routine of agent; or the
// trigger of an offer of agent's on events no routine starts from, which
// routine.ReadEvent reads. Why holds a routine's instruction or an event
// trigger's ask, which the offer's own sentence replaces once read.
func watchOf(t *Trigger, agent string) (*Watch, bool) {
	w := &Watch{
		TriggerID: t.Status.ID, Name: t.Metadata.Name, Title: t.Metadata.DisplayName, SetBy: SetByPerson, Origin: t.Status.Origin,
		Notify: t.Spec.Session.Notify, Paused: t.Spec.Suspend, Pause: t.Status.Paused, LastRunAt: t.Status.LastFiredAt, LastSessionID: t.Status.LastSessionID,
		CreatedAt: t.Status.CreatedAt, SkippedBudget: skippedBudgetOf(t), raw: t.raw,
	}
	if w.Notify == "" {
		w.Notify = NotifyAlways
	}
	if w.Title == "" {
		w.Title = w.Name
	}
	o := t.Status.Origin
	if o != nil && o.Kind == TriggerFromWake {
		w.Kind, w.SetBy, w.Why, w.Notify = WatchWake, SetByAgent, w.Title, NotifyJudged
		w.Session = &WatchSession{ID: o.SessionID}
		if at := t.Spec.At; !at.IsZero() && !w.Paused {
			w.NextAt = &at
		}
		// The trigger a wake counts toward is its session's, read with the
		// session.
		return w, true
	}
	w.CountsToward, w.PerWeekMicroUSD, w.Spend = w.TriggerID, perWeekOf(t), t.Status.Spend
	// A wake continues its session, on the model that session runs on.
	if m := t.Spec.Session.Model; m != nil {
		w.Model = m.Name
	}
	if o != nil && o.Kind == TriggerFromProposal {
		w.SetBy = SetByOffer
		w.Session = &WatchSession{ID: o.SessionID}
	}
	// A routine that runs once fires at its spec's At as a wake does, but
	// with no wake's origin, so it reads as the routine it is.
	if r, ok := RoutineOf(t, agent); ok {
		w.Kind, w.Title, w.Why, w.Starts, w.Schedule, w.NextAt = WatchRoutine, r.Title, r.Instruction, r.Starts, r.Schedule, r.NextRunAt
		w.At, w.AtZone = r.At, r.AtZone
		return w, true
	}
	e, ok := routine.ReadEvent(shapeOf(t), agent)
	if !ok {
		return nil, false
	}
	w.Kind, w.On, w.Title, w.Why = WatchEvent, t.Spec.On, e.Title, e.Ask
	return w, true
}

// ListWatchesParams names the agent whose watches to list, and pages
// them.
type ListWatchesParams struct {
	// Agent is the agent whose watches to list, by name, such as
	// "latere-chat". Required.
	Agent string
	// Limit is how many triggers a page reads, of which the watches are
	// kept, up to 500; 100 when zero. A page may hold fewer watches.
	Limit int
	// Cursor starts ListWatchesPage at the page a previous one's
	// NextCursor named; ListWatches ignores it.
	Cursor string
	// KnownWhys holds the Why of watches set by an offer that an earlier
	// reading returned, by TriggerID. A watch found here is not read from
	// its session's log again: the call an offer was made with never
	// changes, so neither does what it said.
	KnownWhys map[string]string
}

// ListWatches returns an iterator over the caller's watches of
// params.Agent, newest first as the trigger list orders them, reading the
// trigger list a page at a time. An empty Agent yields an error before any
// request.
//
// Besides the page of triggers, a page of watches reads, at most four at
// once:
//
//   - each session a watch of the page came from, once, for its title,
//     and, for a wake, its agent, which the wake's trigger does not name,
//     its Count, and the trigger that started it, which the wake's looks
//     count toward;
//   - for the watches set by an offer whose Why is not in KnownWhys, the
//     log of each session they came from, once, in pages of 500 events
//     from its start up to the last of the calls they were offered with,
//     which hold the sentence of why the trigger does not keep;
//   - after those, each trigger a wake counts toward that is not on the
//     page, once, for its allowance and its Spend.
//
// A program that lists the watches again passes the Why of the last
// reading in KnownWhys, and then reads one page of triggers, one session
// per session the watches came from, and the triggers wakes count toward
// that are on another page. A wake whose session the caller can no longer
// read is left out, since its session's end deletes it; a session gone or
// unreadable otherwise leaves its title empty, and a trigger a wake counts
// toward that is gone or unreadable leaves its allowance and spend empty.
func (c *Client) ListWatches(ctx context.Context, params *ListWatchesParams) iter.Seq2[*Watch, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Watch], error) {
		return c.watchesPage(ctx, params, cursor)
	})
}

// ListWatchesPage returns the watches of params.Agent among one page of
// the caller's triggers, starting at params.Cursor, and the cursor of the
// next page in NextCursor, reading what ListWatches reads for that page.
// A page may hold no watch while NextCursor names another.
func (c *Client) ListWatchesPage(ctx context.Context, params *ListWatchesParams) (*latere.Page[*Watch], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.watchesPage(ctx, params, cursor)
}

func (c *Client) watchesPage(ctx context.Context, params *ListWatchesParams, cursor string) (*latere.Page[*Watch], error) {
	if params == nil || params.Agent == "" {
		return nil, errors.New("agents: ListWatchesParams.Agent is required")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = routinePage
	}
	page, err := c.triggersPage(ctx, &ListTriggersParams{Limit: limit}, cursor)
	if err != nil {
		return nil, err
	}
	var found []*Watch
	byID := map[string]*Trigger{}
	for _, t := range page.Items {
		if t.Status.ID != "" {
			byID[t.Status.ID] = t
		}
		if w, ok := watchOf(t, params.Agent); ok {
			found = append(found, w)
		}
	}
	watches, err := c.readWatchSessions(ctx, params.Agent, found, params.KnownWhys)
	if err != nil {
		return nil, err
	}
	if err := c.readWakeSpends(ctx, watches, byID); err != nil {
		return nil, err
	}
	return &latere.Page[*Watch]{Items: watches, NextCursor: page.NextCursor}, nil
}

// watchReads is how many reads a page of watches has in flight at once.
const watchReads = 4

// eventsPage is how many events a page of a log holds when a watch's
// offer is looked for in it: the most the API answers.
const eventsPage = 500

// clockSlack is how much later than a trigger's creation an event of the
// log its offer was made in may be stamped and still be the offer's call,
// for the clocks of the API's processes, which may differ.
const clockSlack = time.Minute

// readWatchSessions reads, for the watches that came from a session, the
// session once, and the calls of the offers that made those set by an
// offer whose why is not in known, in one pass over each session's log.
// It returns the watches with what was read, without the wakes of
// another agent's sessions or of sessions the caller can no longer read.
func (c *Client) readWatchSessions(ctx context.Context, agent string, watches []*Watch, known map[string]string) ([]*Watch, error) {
	sessions := map[string]*Session{}
	offers := map[string]map[string]*Watch{}
	var order []string
	for _, w := range watches {
		if w.Session == nil {
			continue
		}
		id := w.Session.ID
		if _, seen := sessions[id]; !seen {
			sessions[id] = nil
			order = append(order, id)
		}
		if w.SetBy != SetByOffer {
			continue
		}
		if why, ok := known[w.TriggerID]; ok {
			if why != "" {
				w.Why = why
			}
			continue
		}
		if offers[id] == nil {
			offers[id] = map[string]*Watch{}
		}
		offers[id][w.Origin.ToolUseID] = w
	}
	var mu sync.Mutex
	var tasks []func(context.Context) error
	for _, id := range order {
		tasks = append(tasks, func(ctx context.Context) error {
			s, err := c.GetSession(ctx, id)
			if err != nil && !unreadable(err) {
				return err
			}
			mu.Lock()
			sessions[id] = s
			mu.Unlock()
			return nil
		})
		if calls := offers[id]; calls != nil {
			// Each task writes the Why of the watches of its own session
			// alone.
			tasks = append(tasks, func(ctx context.Context) error { return c.readOffers(ctx, id, calls) })
		}
	}
	if err := runBounded(ctx, tasks); err != nil {
		return nil, err
	}
	out := make([]*Watch, 0, len(watches))
	for _, w := range watches {
		if w.Session != nil {
			s := sessions[w.Session.ID]
			if s != nil {
				w.Session.Title = s.Title
			}
			if w.Kind == WatchWake {
				if s == nil || s.Agent.Name != agent {
					continue
				}
				if s.Wake != nil && s.Wake.TriggerID == w.TriggerID {
					w.Count = s.Wake.Count
				}
				w.CountsToward = s.TriggerID
			}
		}
		out = append(out, w)
	}
	return out, nil
}

// readWakeSpends writes, into each wake of watches that counts toward a
// trigger, that trigger's allowance and Spend: read from page, the page's
// triggers by id, or else from the API, once per trigger. A trigger the
// caller may no longer read leaves them empty.
func (c *Client) readWakeSpends(ctx context.Context, watches []*Watch, page map[string]*Trigger) error {
	counted := map[string]*Trigger{}
	var missing []string
	for _, w := range watches {
		id := w.CountsToward
		if w.Kind != WatchWake || id == "" {
			continue
		}
		if _, seen := counted[id]; seen {
			continue
		}
		counted[id] = page[id]
		if page[id] == nil {
			missing = append(missing, id)
		}
	}
	var mu sync.Mutex
	tasks := make([]func(context.Context) error, 0, len(missing))
	for _, id := range missing {
		tasks = append(tasks, func(ctx context.Context) error {
			t, err := c.GetTrigger(ctx, id)
			if err != nil && !unreadable(err) {
				return err
			}
			mu.Lock()
			counted[id] = t
			mu.Unlock()
			return nil
		})
	}
	if err := runBounded(ctx, tasks); err != nil {
		return err
	}
	for _, w := range watches {
		if w.Kind != WatchWake {
			continue
		}
		if t := counted[w.CountsToward]; t != nil {
			w.PerWeekMicroUSD, w.Spend = perWeekOf(t), t.Status.Spend
		}
	}
	return nil
}

// unreadable reports an answer that the caller may not, or no longer,
// read a session: one deleted, or of a context they left.
func unreadable(err error) bool {
	return errors.Is(err, latere.ErrNotFound) || errors.Is(err, latere.ErrForbidden)
}

// readOffers reads, from the log of session id, the calls the watches of
// calls were offered with, by their ToolUseID, and writes the sentence
// each call wrote of why into its watch's Why. A call the log no longer
// holds, because the session was deleted since or the call is gone from
// it, leaves its watch's Why as it was. Each call came before the
// person's allow made its trigger, so the log is read no further than
// the latest of those.
func (c *Client) readOffers(ctx context.Context, id string, calls map[string]*Watch) error {
	var last time.Time
	for _, w := range calls {
		if w.CreatedAt.IsZero() {
			last = time.Time{}
			break
		}
		if w.CreatedAt.After(last) {
			last = w.CreatedAt
		}
	}
	left := len(calls)
	for e, err := range c.ListEvents(ctx, id, &ListEventsParams{Limit: eventsPage}) {
		if err != nil {
			if unreadable(err) {
				return nil
			}
			return err
		}
		if !last.IsZero() && e.Time.After(last.Add(clockSlack)) {
			return nil
		}
		if e.Type != EventAgentToolUse || e.Thread != "" {
			continue
		}
		var use ToolUse
		if err := e.Decode(&use); err != nil {
			return err
		}
		w := calls[use.ToolUseID]
		if w == nil {
			continue
		}
		offer, err := use.Offer()
		if err != nil {
			return err
		}
		if offer.Why != "" {
			w.Why = offer.Why
		}
		if left--; left == 0 {
			return nil
		}
	}
	return nil
}

// runBounded runs tasks, at most watchReads at once, and returns the first
// error one returns, after which the others' context is canceled and no
// more start.
func runBounded(ctx context.Context, tasks []func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	slots := make(chan struct{}, watchReads)
	var wg sync.WaitGroup
	for _, task := range tasks {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			if err := task(ctx); err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return context.Cause(ctx)
}
