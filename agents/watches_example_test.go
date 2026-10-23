// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// Listing what watches for the person in Latere Chat, whether everything
// is paused, and each watch's week against its allowance. The program
// keeps the whys of the last reading, so the next one reads no session's
// log again.
func ExampleClient_ListWatches() {
	ctx := context.Background()
	c := client()
	hold, err := c.GetTriggerHold(ctx)
	if err != nil && !errors.Is(err, latere.ErrForbidden) {
		log.Fatal(err)
	}
	if hold != nil && hold.Held {
		fmt.Println("Everything is paused since", hold.Since.Local())
	}
	whys := map[string]string{}
	for w, err := range c.ListWatches(ctx, &agents.ListWatchesParams{Agent: "latere-chat", KnownWhys: whys}) {
		if err != nil {
			log.Fatal(err)
		}
		whys[w.TriggerID] = w.Why
		switch w.Kind {
		case agents.WatchWake:
			fmt.Printf("%s: looks again %s in %q (%d in a row)\n", w.Title, w.NextAt.Local(), w.Session.Title, w.Count)
		case agents.WatchEvent:
			fmt.Printf("%s: on each %s event. %s\n", w.Title, w.On.Product, w.Why)
		default:
			fmt.Printf("%s: %s\n", w.Title, w.Why)
		}
		if w.SetBy == agents.SetByOffer {
			fmt.Println("  offered in", w.Session.ID, "and accepted on", w.CreatedAt.Local().Format("2 January"))
		}
		if s := w.Spend; s != nil && w.PerWeekMicroUSD > 0 {
			fmt.Printf("  $%.2f of $%.2f this week\n", float64(s.SpentMicroUSD)/1e6, float64(w.PerWeekMicroUSD)/1e6)
			switch {
			case s.AtAllowance():
				fmt.Println("  paused by its allowance until older looks leave the week")
			case s.AheadOfPace():
				fmt.Printf("  on course for $%.2f\n", float64(*s.ProjectedMicroUSD)/1e6)
			}
		}
	}
}

// Giving a watch an allowance of $2 a week, such as an event trigger the
// person accepted from an offer that named none.
func ExampleClient_SetTriggerAllowance() {
	ctx := context.Background()
	t, err := client().SetTriggerAllowance(ctx, "trg_01M34G2JF01B91B8399CD1G6YP", 2_000_000)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(t.Metadata.DisplayName, t.Spec.Budget.PerWeekMicroUSD)
}

// Pausing every trigger of the person at once. Each keeps its own pause,
// so releasing the hold brings back only those that were on.
func ExampleClient_SetTriggerHold() {
	ctx := context.Background()
	hold, err := client().SetTriggerHold(ctx, true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("paused since", hold.Since)
}

// Showing the offer a session waits on, and sending the person's answer:
// allow, deny, or a deny that says in their words what to change, which
// the agent offers again from.
func ExampleToolUse_Offer() {
	ctx := context.Background()
	c := client()
	const id = "ses_01M34G2JF01B91B8399CD1G6YP"
	var held *agents.Offer
	for e, err := range c.ListEvents(ctx, id, nil) {
		if err != nil {
			log.Fatal(err)
		}
		if e.Type != agents.EventAgentToolUse {
			continue
		}
		var use agents.ToolUse
		if err := e.Decode(&use); err != nil {
			log.Fatal(err)
		}
		if use.Verdict != agents.VerdictAsk {
			continue
		}
		if o, err := use.Offer(); err == nil {
			held = o
		}
	}
	if held == nil {
		return
	}
	switch held.Action {
	case agents.OfferPropose:
		fmt.Println(held.Name+"?", held.Why)
		if held.PerWeekUSD != "" {
			fmt.Println("At most $" + held.PerWeekUSD + " a week.")
		}
	case agents.OfferChange, agents.OfferResume, agents.OfferDelete:
		t, err := c.GetTrigger(ctx, held.TriggerID)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(held.Action, t.Metadata.DisplayName+"?", held.Why)
	case agents.OfferWake:
		fmt.Println("Keep watching:", held.Name+"?")
	}
	_, err := c.ConfirmTool(ctx, id, &agents.ConfirmToolParams{
		ToolUseID: held.ToolUseID,
		Decision:  agents.DecisionDeny,
		Note:      "Make it 8:00.",
	})
	if err != nil {
		log.Fatal(err)
	}
}
