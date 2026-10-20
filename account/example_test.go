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

func client() *account.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return account.New(cfg)
}

// Showing the credit left. A member of an organization sees no amount,
// and the organization's monthly limit may say when turns open again.
func ExampleClient_GetWallet() {
	w, err := client().GetWallet(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	switch {
	case w.State == account.WalletExhausted && !w.LimitResetsAt.IsZero():
		fmt.Println("the organization's limit opens again on", w.LimitResetsAt.Format("January 2"))
	case w.State == account.WalletExhausted:
		fmt.Println("the wallet is empty")
	case w.SpendableMicroUSD != nil:
		fmt.Printf("%.2f USD left\n", float64(*w.SpendableMicroUSD)/1e6)
	default:
		fmt.Println("the organization pays")
	}
}

// Listing the Effort levels a session can run at now.
func ExampleClient_GetEffortLevels() {
	levels, err := client().GetEffortLevels(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, l := range levels.Levels {
		if l.Available {
			fmt.Println(l.ID, l.Name)
		} else {
			fmt.Println(l.ID, "unavailable:", l.Reason)
		}
	}
}

// Finding the person's computers that sessions can work on now.
func ExampleClient_ListDevices() {
	devices, err := client().ListDevices(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range devices.Items {
		if d.Status == account.DeviceOnline {
			fmt.Println(d.Name, d.ID)
		}
	}
}

// Making sure the caller has their copy of a managed agent before creating
// a session with it.
func ExampleClient_ApplyManagedAgent() {
	applied, err := client().ApplyManagedAgent(context.Background(), "latere-chat")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(applied.ID, applied.Version)
}
