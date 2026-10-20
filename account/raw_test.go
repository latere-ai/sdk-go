// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"testing"

	"latere.ai/x/sdk-go/account"
)

// Each resource the package answers keeps the JSON it was decoded from, a
// field the package does not model included.
func TestEveryResourceKeepsItsJSON(t *testing.T) {
	for name, v := range map[string]interface {
		RawJSON() json.RawMessage
	}{
		"applied agent": &account.AppliedAgent{},
		"effort levels": &account.EffortLevels{},
		"wallet":        &account.Wallet{},
		"preferences":   &account.Preferences{},
		"device":        &account.Device{},
	} {
		if v.RawJSON() != nil {
			t.Errorf("%s: one the program built has JSON", name)
		}
		raw := `{"added_later":"kept"}`
		if err := json.Unmarshal([]byte(raw), v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(v.RawJSON()) != raw {
			t.Errorf("%s: RawJSON = %s, want %s", name, v.RawJSON(), raw)
		}
	}
}

// The wallet answered by the API keeps its JSON alongside the decoded
// fields.
func TestTheWalletKeepsTheJSONTheAPIAnswered(t *testing.T) {
	raw := `{"state":"Open","spendable_micro":4200000,"added_later":"kept"}`
	c, _ := fakeAPI(t, map[string]string{"GET /v1/account/wallet": raw})
	w, err := c.GetWallet(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if string(w.RawJSON()) != raw || w.SpendableMicroUSD == nil || *w.SpendableMicroUSD != 4200000 {
		t.Errorf("wallet %+v with %s; want the decoded amount and the whole answer", w, w.RawJSON())
	}
}
