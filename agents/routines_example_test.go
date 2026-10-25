// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// Making a routine of Latere Chat's agent that runs every Monday and
// Thursday at 9:00 in Berlin, for at most $1 a week. The key comes from
// the person's press of the button, so a create sent again after a lost
// answer makes no second routine.
func ExampleClient_CreateRoutine() {
	ctx := context.Background()
	r, err := client().CreateRoutine(ctx, &agents.CreateRoutineParams{
		Agent:       "latere-chat",
		Title:       "Weekly news",
		Instruction: "Summarize this week's news on climate policy and city cycling.",
		Schedule:    &agents.RoutineSchedule{Days: []time.Weekday{time.Monday, time.Thursday}, Hour: 9, Zone: "Europe/Berlin"},
		// At most $1 a week; the API starts no run past it.
		PerWeekMicroUSD: 1_000_000,
		// IdempotencyKey: the key the program made for this press.
	})
	if errors.Is(err, agents.ErrRoutineTitleMissing) {
		fmt.Println("Give the routine a name.")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(r.Name, r.NextRunAt)
}

// Making a routine on dates: it checks in for an outbound flight and the
// return, at 9:30 the day before each, in the zone the person picked them
// in, on the Thorough Effort level, telling the person only when a run
// finds something that needs them. The API deletes it after the last
// date; DatesZone is kept so the routine read back says the dates as the
// person picked them.
func ExampleClient_CreateRoutine_dates() {
	ctx := context.Background()
	zone := "Europe/Berlin"
	berlin, err := time.LoadLocation(zone)
	if err != nil {
		log.Fatal(err)
	}
	r, err := client().CreateRoutine(ctx, &agents.CreateRoutineParams{
		Agent:       "latere-chat",
		Title:       "Flight check-in",
		Instruction: "Check whether check-in for my flight is open, and tell me if it is.",
		Starts:      agents.StartsOnDates,
		Dates: []time.Time{
			time.Date(2026, 11, 3, 9, 30, 0, 0, berlin),
			time.Date(2026, 11, 10, 9, 30, 0, 0, berlin),
		},
		DatesZone: zone,
		Model:     "tier/thorough",
		Notify:    agents.NotifyJudged,
	})
	if errors.Is(err, agents.ErrRoutineDatesInvalid) {
		fmt.Println("Pick at least one time that has not passed.")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range r.Dates {
		fmt.Println(d.In(berlin).Format("Mon 2 Jan 15:04"))
	}
}

// Listing Latere Chat's routines with when each runs next.
func ExampleClient_ListRoutines() {
	ctx := context.Background()
	for r, err := range client().ListRoutines(ctx, &agents.ListRoutinesParams{Agent: "latere-chat"}) {
		if err != nil {
			log.Fatal(err)
		}
		switch {
		case r.Paused:
			fmt.Println(r.Title, "paused")
		case r.NextRunAt != nil:
			fmt.Println(r.Title, r.NextRunAt.Local())
		default:
			fmt.Println(r.Title, "runs when something arrives")
		}
	}
}

// Pausing a routine. Every field of its trigger the change does not name
// is kept as it was.
func ExampleClient_ChangeRoutine() {
	ctx := context.Background()
	paused := true
	_, err := client().ChangeRoutine(ctx, "latere-chat", "routine-3f2a9c1b7d4e5f60a1b2", &agents.ChangeRoutineParams{Paused: &paused})
	if errors.Is(err, latere.ErrNotFound) {
		fmt.Println("There is no such routine.")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
}

// Running a routine now, outside its schedule, and opening the session
// the run started.
func ExampleClient_RunRoutine() {
	ctx := context.Background()
	f, err := client().RunRoutine(ctx, "latere-chat", "routine-3f2a9c1b7d4e5f60a1b2", nil)
	if errors.Is(err, agents.ErrRoutinePaused) {
		fmt.Println("Resume the routine to run it.")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(f.Outcome, f.SessionID)
}

// Deleting a routine. The sessions its runs started stay.
func ExampleClient_DeleteRoutine() {
	ctx := context.Background()
	if err := client().DeleteRoutine(ctx, "latere-chat", "routine-3f2a9c1b7d4e5f60a1b2"); err != nil && !errors.Is(err, latere.ErrNotFound) {
		log.Fatal(err)
	}
}

// The sessions a routine's runs started, newest first, archived ones too.
func ExampleRoutine() {
	ctx := context.Background()
	c := client()
	r, err := c.GetRoutine(ctx, "latere-chat", "routine-3f2a9c1b7d4e5f60a1b2")
	if err != nil {
		log.Fatal(err)
	}
	for s, err := range c.ListSessions(ctx, &agents.ListSessionsParams{Trigger: r.TriggerID, Archived: agents.ArchivedIncluded}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.CreatedAt, s.Status)
	}
}
