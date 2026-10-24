// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

// forecastAnswer is the route's own example, with a follow-up that waits
// for the pool and one in the person's own conversation.
const forecastAnswer = `{"zone":"Europe/Berlin","today":"2026-10-24",
"days":[{"date":"2026-10-23","person_led_micro":610000,"proactive_micro":140000,"expected_person_led_micro":0,"expected_proactive_micro":0},
{"date":"2026-10-24","person_led_micro":210000,"proactive_micro":60000,"expected_person_led_micro":260000,"expected_proactive_micro":0}],
"person_led_per_day_micro":520000,"last_month_micro":18400000,"unlisted_proactive_micro":5000,"spendable_micro":5000000,"share_bps":2000,
"pool_micro":1124000,"proactive_week_micro":620000,"runs_out_on":"2026-10-29","proactive_skips_from":"2026-10-28",
"proactive_again_at":"2026-10-27T08:01:00Z","future":"kept",
"items":[{"trigger_id":"trg_1","fires_on":"schedule","paused":false,"half":"proactive","per_week_micro":400000,"per_run_micro":60000,
"spent_micro":1100000,"runs":9,"days":[{"date":"2026-10-24","ran":1,"skipped":0,"expected":0,"expected_skipped":0}],
"forecast_micro":1340000,"expected_at":["2026-10-25T07:00:00Z"],"expected_runs":7,"allowance_skips":3,"share_skips":0,
"skipping":{"why":"allowance","until":"2026-10-27T07:00:00Z"}},
{"trigger_id":"trg_2","fires_on":"at","paused":false,"half":"proactive","counted_in":"trg_1","per_run_micro":0,"spent_micro":0,"runs":0,
"days":[],"forecast_micro":0,"expected_at":["2026-10-27T08:01:00Z"],"expected_runs":1,"allowance_skips":0,"share_skips":0,
"skipping":{"why":"share","until":"2026-10-27T08:01:00Z"}},
{"trigger_id":"trg_3","fires_on":"event","paused":true,"half":"person_led","spent_micro":30000,"runs":2,"days":[],"most_micro":1600000,
"per_day":2.5,"expected_runs":0,"allowance_skips":0,"share_skips":0}]}`

// TestTheForecastIsReadInAZone: the forecast reads its calendar dates as
// midnight UTC of each date, its moments as times, each item with its
// half, where it is counted, what its month comes to and where its next
// run is skipped or waits; a per-run cost of nothing is read as such; the
// zone asked is sent, and none for UTC.
func TestTheForecastIsReadInAZone(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, forecastAnswer)
	})
	f, err := c.GetForecast(t.Context(), &account.GetForecastParams{TimeZone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	if f.Zone != "Europe/Berlin" || !f.Today.Equal(day(24)) || len(f.Days) != 2 || !f.Days[1].Date.Equal(day(24)) || f.Days[1].ExpectedPersonLedMicroUSD != 260_000 ||
		f.PersonLedPerDayMicroUSD != 520_000 || f.LastMonthMicroUSD != 18_400_000 || f.UnlistedProactiveMicroUSD != 5000 || f.ShareBasisPoints != 2000 ||
		f.SpendableMicroUSD != 5_000_000 || f.PoolMicroUSD != 1_124_000 || f.ProactiveWeekMicroUSD != 620_000 {
		t.Fatalf("the forecast reads %+v", f)
	}
	if f.RunsOutOn == nil || !f.RunsOutOn.Equal(day(29)) || f.ProactiveSkipsFrom == nil || !f.ProactiveSkipsFrom.Equal(day(28)) ||
		f.ProactiveAgainAt == nil || !f.ProactiveAgainAt.Equal(time.Date(2026, 10, 27, 8, 1, 0, 0, time.UTC)) {
		t.Fatalf("the month's dates read %v, %v, %v", f.RunsOutOn, f.ProactiveSkipsFrom, f.ProactiveAgainAt)
	}
	if len(f.Items) != 3 {
		t.Fatalf("%d items", len(f.Items))
	}
	d := f.Items[0]
	if d.TriggerID != "trg_1" || d.FiresOn != account.FiresOnSchedule || d.Half != account.HalfProactive || d.PerWeekMicroUSD != 400_000 || *d.PerRunMicroUSD != 60_000 ||
		d.SpentMicroUSD != 1_100_000 || d.Runs != 9 || *d.ForecastMicroUSD != 1_340_000 || d.MostMicroUSD != nil || len(d.ExpectedAt) != 1 || d.ExpectedRuns != 7 ||
		d.AllowanceSkips != 3 || d.Skipping == nil || d.Skipping.Why != account.SkipAllowance || !d.Skipping.Until.Equal(time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)) ||
		len(d.Days) != 1 || !d.Days[0].Date.Equal(day(24)) || d.Days[0].Ran != 1 || !d.InProactiveHalf() {
		t.Fatalf("the schedule's month: %+v", d)
	}
	w := f.Items[1]
	if w.FiresOn != account.FiresOnAt || w.CountedIn != "trg_1" || w.PerRunMicroUSD == nil || *w.PerRunMicroUSD != 0 || w.InProactiveHalf() ||
		w.Skipping.Why != account.SkipShare || !w.Skipping.Until.Equal(*f.ProactiveAgainAt) {
		t.Fatalf("the follow-up that waits: %+v", w)
	}
	e := f.Items[2]
	if e.FiresOn != account.FiresOnEvent || !e.Paused || e.Half != account.HalfPersonLed || e.PerRunMicroUSD != nil || e.ForecastMicroUSD != nil ||
		*e.MostMicroUSD != 1_600_000 || e.PerDay != 2.5 || e.Skipping != nil || e.InProactiveHalf() {
		t.Fatalf("the paused event trigger: %+v", e)
	}
	var none *account.ForecastItem
	if none.InProactiveHalf() {
		t.Error("a nil item is in the proactive half")
	}
	var raw map[string]any
	if err := json.Unmarshal(f.RawJSON(), &raw); err != nil || raw["future"] != "kept" {
		t.Errorf("the raw forecast: %v, %v", raw, err)
	}
	if _, err := c.GetForecast(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 2 || got[0].Method != http.MethodGet || got[0].Path != "/v1/account/wallet/forecast" || got[0].Query != "tz=Europe%2FBerlin" || got[1].Query != "" {
		t.Errorf("sent %+v", got)
	}
}

// TestAForecastThatCannotBeRead: a forecast the deployment cannot give yet
// is the API's error with its code, an organization's context is
// forbidden, and a date in no date's shape fails the read.
func TestAForecastThatCannotBeRead(t *testing.T) {
	status, body := http.StatusServiceUnavailable, `{"error":{"code":"forecast_unavailable","message":"The forecast is not available right now."}}`
	c, _ := capturingAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	})
	_, err := c.GetForecast(t.Context(), nil)
	if e, ok := errors.AsType[*latere.Error](err); !ok || e.Code != "forecast_unavailable" {
		t.Fatalf("a forecast the deployment cannot give: %v", err)
	}
	status, body = http.StatusForbidden, `{"error":{"code":"forbidden","message":"The forecast is shown for your personal account."}}`
	if _, err := c.GetForecast(t.Context(), nil); !errors.Is(err, latere.ErrForbidden) {
		t.Fatalf("an organization's context: %v", err)
	}
	for _, bad := range []string{
		`{"today":"Saturday"}`,
		`{"today":"2026-10-24","runs_out_on":"soon"}`,
		`{"today":"2026-10-24","proactive_skips_from":"2026-10-28T00:00:00Z"}`,
		`{"today":"2026-10-24","days":[{"date":"24 Oct"}]}`,
		`{"today":"2026-10-24","items":[{"trigger_id":"trg_1","days":[{"date":""}]}]}`,
		`{"today":1}`,
	} {
		var f account.Forecast
		if err := json.Unmarshal([]byte(bad), &f); err == nil {
			t.Errorf("%s was read", bad)
		}
	}
}
