// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

// spendAnswer is the route's own example.
const spendAnswer = `{"days":[{"date":"2026-10-22","person_led_micro":84000,"proactive_micro":12000},
{"date":"2026-10-23","person_led_micro":120000,"proactive_micro":30000}],"share_bps":2000,"share_moved_at":"2026-10-20T09:00:00Z",
"spendable_micro":1000000,"proactive_week_micro":100000,"pool_micro":220000}`

// TestTheSpendIsReadByDay: the spend reads each day at midnight UTC with
// what the person led and what ran with nobody watching, the share, the
// pool and the week, and what proactive work may still spend; the days
// asked are sent, and none for the API's default.
func TestTheSpendIsReadByDay(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, spendAnswer)
	})
	s, err := c.GetSpend(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []account.SpendDay{
		{Date: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), PersonLedMicroUSD: 84_000, ProactiveMicroUSD: 12_000},
		{Date: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), PersonLedMicroUSD: 120_000, ProactiveMicroUSD: 30_000},
	}
	if !reflect.DeepEqual(s.Days, want) || s.ShareBasisPoints != 2000 || !s.ShareMovedAt.Equal(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)) ||
		s.SpendableMicroUSD != 1_000_000 || s.ProactiveWeekMicroUSD != 100_000 || s.PoolMicroUSD != 220_000 || len(s.RawJSON()) == 0 {
		t.Fatalf("the spend reads %+v", s)
	}
	if left := s.ProactiveLeftMicroUSD(); left != 120_000 {
		t.Errorf("proactive work has %d left", left)
	}
	if _, err := c.GetSpend(t.Context(), &account.GetSpendParams{Days: account.MaxSpendDays}); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 2 || got[0].Method != http.MethodGet || got[0].Path != "/v1/account/wallet/spend" || got[0].Query != "" || got[1].Query != "days=90" {
		t.Errorf("sent %+v", got)
	}
	b, err := json.Marshal(s.Days[0])
	if err != nil || string(b) != `{"date":"2026-10-22","person_led_micro":84000,"proactive_micro":12000}` {
		t.Errorf("a day is written as %s, %v", b, err)
	}
}

// TestTheSpendIsRefusedOutsideItsBounds: days outside 1 to MaxSpendDays
// reach no API; a day the API writes in no date's shape fails the read;
// an organization's context is the API's forbidden; and nothing proactive
// is left once the week reaches the pool.
func TestTheSpendIsRefusedOutsideItsBounds(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"forbidden","message":"Spending over time is shown for your personal account."}}`)
	})
	for _, days := range []int{-1, account.MaxSpendDays + 1} {
		if _, err := c.GetSpend(t.Context(), &account.GetSpendParams{Days: days}); err == nil {
			t.Errorf("%d days were read", days)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("days out of bounds sent %d requests", n)
	}
	if _, err := c.GetSpend(t.Context(), nil); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("an organization's context: %v", err)
	}
	var s account.Spend
	if err := json.Unmarshal([]byte(`{"days":[{"date":"2026-10-22T00:00:00Z","person_led_micro":1,"proactive_micro":0}]}`), &s); err == nil {
		t.Error("a day written as a timestamp was read")
	}
	if err := json.Unmarshal([]byte(`{"days":[{"date":1}]}`), &s); err == nil {
		t.Error("a day written as a number was read")
	}
	if left := (&account.Spend{PoolMicroUSD: 100, ProactiveWeekMicroUSD: 130}).ProactiveLeftMicroUSD(); left != 0 {
		t.Errorf("past the pool, %d is left", left)
	}
	var none *account.Spend
	if none.ProactiveLeftMicroUSD() != 0 {
		t.Error("a nil spend has some left")
	}
}

// routesOutsideTheDocument are the routes the package calls that the
// published account document does not describe yet, each with what its
// models follow instead.
var routesOutsideTheDocument = map[string]string{
	"get /v1/account/wallet/spend": "the spending read, which Spend and SpendDay follow as the platform's own account reference describes it, ahead of the published document",
}

// TestRoutesOutsideTheDocumentAreStillOutside fails once the vendored
// account document describes one of routesOutsideTheDocument, so its
// models are then held to the document like the rest of the package's.
func TestRoutesOutsideTheDocumentAreStillOutside(t *testing.T) {
	raw, err := os.ReadFile("../openapi/account.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	for route := range routesOutsideTheDocument {
		method, path, _ := strings.Cut(route, " ")
		if _, ok := d.Paths[path][method]; ok {
			t.Errorf("%s is now in the account document: hold Spend and SpendDay to it and take it off routesOutsideTheDocument", route)
		}
	}
}
