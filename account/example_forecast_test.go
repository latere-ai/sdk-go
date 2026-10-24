// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

// Showing what a person will have spent by the end of the month, their
// own conversations and what keeps running for them apart, and each
// item's month.
func ExampleClient_GetForecast() {
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	f, err := account.New(cfg).GetForecast(context.Background(), &account.GetForecastParams{TimeZone: "Europe/Berlin"})
	if err != nil {
		log.Fatal(err)
	}
	var own, always int64
	for _, d := range f.Days {
		own += d.PersonLedMicroUSD + d.ExpectedPersonLedMicroUSD
		always += d.ProactiveMicroUSD + d.ExpectedProactiveMicroUSD
	}
	fmt.Printf("by the month's end: about $%.2f your conversations, $%.2f always on\n", float64(own)/1e6, float64(always)/1e6)
	if f.RunsOutOn != nil {
		fmt.Printf("at this rate your credit runs out around %s\n", f.RunsOutOn.Format("Jan 2"))
	}
	for _, it := range f.Items {
		switch {
		case it.ForecastMicroUSD != nil:
			fmt.Printf("%s: about $%.2f this month\n", it.TriggerID, float64(*it.ForecastMicroUSD)/1e6)
		case it.MostMicroUSD != nil:
			fmt.Printf("%s: no runs yet, at most $%.2f\n", it.TriggerID, float64(*it.MostMicroUSD)/1e6)
		}
	}
}
