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

// Showing a person's last week of spending, split into what they led and
// what ran with nobody watching, and what proactive work may still spend.
func ExampleClient_GetSpend() {
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	s, err := account.New(cfg).GetSpend(context.Background(), &account.GetSpendParams{Days: 7})
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range s.Days {
		fmt.Printf("%s  you $%.2f  routines $%.2f\n", d.Date.Format("Mon 2 Jan"), float64(d.PersonLedMicroUSD)/1e6, float64(d.ProactiveMicroUSD)/1e6)
	}
	fmt.Printf("routines may spend $%.2f more this week (%d%% of your credit)\n", float64(s.ProactiveLeftMicroUSD())/1e6, s.ShareBasisPoints/100)
}
