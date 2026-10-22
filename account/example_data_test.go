// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/account"
)

// Saying where the installation keeps sessions, and saying so when its
// operator has not stated it.
func ExampleClient_GetDataPlaces() {
	d, err := client().GetDataPlaces(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	if d.Sessions == nil {
		fmt.Println("where sessions are kept is not stated")
		return
	}
	fmt.Println("sessions are kept in", d.Sessions.Place, d.Sessions.Country)
}

// Telling a person whether a request to the model that ran a turn may be
// kept by a provider.
func ExampleClient_GetModelDataTerms() {
	terms, err := client().GetModelDataTerms(context.Background(), "anthropic/claude-sonnet-5.5")
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range terms.Models {
		name := m.DisplayName
		if name == "" {
			name = m.Name
		}
		switch m.Retention {
		case "none":
			fmt.Println(name, "is served by providers that keep nothing")
		case "provider":
			fmt.Println(name, "may be kept by a provider that serves it")
		default:
			fmt.Println(name, "has no known terms")
		}
	}
}

// Opening a payment page for 10 USD of credit, which returns the person
// to the page they were on.
func ExampleClient_StartCheckout() {
	page, err := client().StartCheckout(context.Background(), &account.StartCheckoutParams{
		AmountMicroUSD: 10_000_000,
		SuccessURL:     "https://chat.latere.ai/?credit=added",
		CancelURL:      "https://chat.latere.ai/",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("send the person to", page.URL)
}

// Recording, at a person's first sign-in, the site that referred them and
// the page they landed on.
func ExampleClient_RecordArrival() {
	err := client().RecordArrival(context.Background(), &account.RecordArrivalParams{
		SourceHost:  "news.ycombinator.com",
		LandingPath: "/blog/why-latere",
	})
	if err != nil {
		log.Fatal(err)
	}
}
