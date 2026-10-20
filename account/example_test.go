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

// Showing the credit left.
func ExampleClient_Wallet() {
	w, err := client().Wallet(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	if w.State == account.WalletExhausted {
		fmt.Println("the wallet is empty")
		return
	}
	fmt.Printf("%.2f USD left\n", float64(w.SpendableMicroUSD)/1e6)
}

// Listing the Effort levels a session can run at now.
func ExampleClient_EffortLevels() {
	levels, err := client().EffortLevels(context.Background())
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
func ExampleClient_Devices() {
	devices, err := client().Devices(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range devices.Items {
		if d.Online {
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
