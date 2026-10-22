// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

func fakeAPI(t *testing.T, routes map[string]string) (*account.Client, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":{"code":"device_person_required","message":"Only a person's own token may do this."}}`)
			return
		}
		if body == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return account.New(cfg), &calls
}

func TestReads(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"PUT /v1/account/agents/latere-chat": `{"id":"latere-chat","version":7,"model":"tier/quick","agent":{"id":"agent_1","version":3}}`,
		"GET /v1/account/effort": `{"levels":[{"id":"quick","name":"tier/quick","order":0,"available":true,"retention":"none",
			"next":{"charge":"credit","price":{"currency":"USD","per":1000000,"input":"0.25","output":"1.25"}},
			"free_turns":{"bound":50,"used":3,"resets_at":"2026-10-21T00:00:00Z"},"models":[{"name":"m","charge":"none","available":true}]}],
			"auto":{"name":"auto","available":true,"levels":["quick","thorough"],"reads":"message","retention":"none"},"zero_retention":true,"zero_retention_by":"person"}`,
		"GET /v1/account/wallet": `{"state":"Open","spendable_micro":4200000,"bought":true,"next_grant":{"amount_micro":1000000,"at":"2026-11-01T00:00:00Z"},
			"top_up":{"min_micro":5000000,"spread_bps":290,"spread_fixed_micro":300000}}`,
		"GET /v1/account/preferences": `{"network":{"mode":"listed","domains":["example.com"],"managed_by":"you","editable":true,"modes":["listed","open"],"package_managers":["npm"],"max_domains":50},
			"sharing":{"mode":"anyone","managed_by":"you","editable":false,"modes":["anyone"]},
			"profile":{"call_me":"Al","work":"research","works":["research"],"max_call_me":40},"instructions":{"text":"Be brief.","max_chars":4000},
			"pins":{"sessions":["ses_1"],"apps":[],"projects":[],"max_sessions":20,"max_apps":20,"max_projects":20},
			"bookmarks":{"sessions":{"ses_1":[3,9]},"max_per_session":50,"max_sessions":100},"data":{"zero_retention":false}}`,
		"PUT /v1/account/preferences/pins/ses_1":    `{"sessions":["ses_1"],"apps":[],"projects":[],"max_sessions":20,"max_apps":20,"max_projects":20}`,
		"DELETE /v1/account/preferences/pins/ses_1": "",
		"GET /v1/account/devices": `{"items":[{"id":"dev_1","name":"MacBook","status":"online","online":true,"os":"darwin","arch":"arm64","isolation":"process",
			"folders":[{"path":"/Users/al/code","write":true}],"network":{"mode":"allowlist","hosts":["github.com"],"blocked":["ads.example.com"]},"created_at":"2026-10-19T10:00:00Z"}],"limit":5}`,
	})
	applied, err := c.ApplyManagedAgent(t.Context(), "latere-chat")
	if err != nil || applied.Agent.ID != "agent_1" || applied.Model != "tier/quick" {
		t.Errorf("ApplyManagedAgent: %+v, %v", applied, err)
	}
	levels, err := c.GetEffortLevels(t.Context())
	if err != nil || levels.Levels[0].Next.Price.Output != "1.25" || levels.Levels[0].FreeTurns.Used != 3 || levels.Auto.Reads != "message" || !levels.ZeroRetention ||
		levels.Levels[0].FreeTurns.ResetsAt.IsZero() || !levels.Levels[0].ResetsAt.IsZero() {
		t.Errorf("EffortLevels: %+v, %v", levels, err)
	}
	w, err := c.GetWallet(t.Context())
	if err != nil || w.State != account.WalletOpen || w.SpendableMicroUSD == nil || *w.SpendableMicroUSD != 4200000 || w.NextGrant.AmountMicroUSD != 1000000 ||
		w.TopUp.SpreadBasisPoints != 290 || !w.NextGrant.At.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Wallet: %+v, %v", w, err)
	}
	p, err := c.GetPreferences(t.Context())
	if err != nil || p.Network.Domains[0] != "example.com" || p.Profile.CallMe != "Al" || p.Pins.Sessions[0] != "ses_1" || p.Bookmarks.Sessions["ses_1"][1] != 9 {
		t.Errorf("Preferences: %+v, %v", p, err)
	}
	pins, err := c.PinSession(t.Context(), "ses_1")
	if err != nil || pins.MaxSessions != 20 {
		t.Errorf("PinSession: %+v, %v", pins, err)
	}
	if err := c.UnpinSession(t.Context(), "ses_1"); err != nil {
		t.Errorf("UnpinSession: %v", err)
	}
	d, err := c.ListDevices(t.Context())
	if err != nil || d.Limit != 5 || d.Items[0].Folders[0].Path != "/Users/al/code" || d.Items[0].Network.Hosts[0] != "github.com" || d.Items[0].Network.Blocked[0] != "ads.example.com" ||
		d.Items[0].Status != account.DeviceOnline || d.Items[0].CreatedAt.IsZero() || !d.Items[0].LastSeen.IsZero() {
		t.Errorf("Devices: %+v, %v", d, err)
	}
	if len(*calls) != 7 {
		t.Errorf("sent %d requests", len(*calls))
	}
}

func TestAnOrganizationMembersWallet(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{
		"GET /v1/account/wallet": `{"state":"Exhausted","limit_resets_at":"2026-11-01T00:00:00Z"}`,
	})
	w, err := c.GetWallet(t.Context())
	if err != nil || w.SpendableMicroUSD != nil || w.State != account.WalletExhausted || w.LimitResetsAt.IsZero() {
		t.Errorf("a member's wallet: %+v, %v; want no amount and the limit's reset", w, err)
	}
}

func TestAClientWithoutAConfig(t *testing.T) {
	if _, err := account.New(nil).GetWallet(t.Context()); err == nil {
		t.Error("a call without a Config succeeded")
	}
}

func TestErrors(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{})
	var apiErr *latere.Error
	if _, err := c.ListDevices(t.Context()); !errors.As(err, &apiErr) || apiErr.Code != "device_person_required" || !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("Devices with a key: %v", err)
	}
	for name, err := range map[string]error{
		"ApplyManagedAgent empty": second(c.ApplyManagedAgent(t.Context(), "")),
		"ApplyManagedAgent":       second(c.ApplyManagedAgent(t.Context(), "x")),
		"EffortLevels":            second(c.GetEffortLevels(t.Context())),
		"Wallet":                  second(c.GetWallet(t.Context())),
		"Preferences":             second(c.GetPreferences(t.Context())),
		"PinSession empty":        second(c.PinSession(t.Context(), "")),
		"PinSession":              second(c.PinSession(t.Context(), "ses_1")),
		"UnpinSession empty":      c.UnpinSession(t.Context(), ""),
		"UnpinSession":            c.UnpinSession(t.Context(), "ses_1"),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }
