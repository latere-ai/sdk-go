// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/oversight"
)

// calibrationReport is a report as the API answers it: an empty bin, a
// bin with too few checks, a calibrated one, an overconfident one and an
// underconfident one, the other bins empty, and a field the package does
// not model.
func calibrationReport(errorValue string) string {
	bins := make([]string, oversight.CalibrationBinCount)
	for i := range bins {
		from, to := float64(i)/10, float64(i+1)/10
		bins[i] = fmt.Sprintf(`{"from":%g,"to":%g,"cases":0,"covered":0,"stated":null,"observed":null,"reviews":0,"considered":0,"state":"not_estimable"}`, from, to)
	}
	bins[2] = `{"from":0.2,"to":0.3,"cases":6,"covered":1,"stated":0.24,"observed":null,"reviews":6,"considered":4,"state":"not_estimable"}`
	bins[5] = `{"from":0.5,"to":0.6,"cases":40,"covered":1,"stated":0.55,"observed":0.6,"interval":[0.45,0.73],"reviews":40,"considered":31,"state":"calibrated"}`
	bins[8] = `{"from":0.8,"to":0.9,"cases":90,"covered":0.5,"stated":0.86,"observed":0.62,"interval":[0.5,0.72],"reviews":45,"considered":20,"state":"overconfident"}`
	bins[9] = `{"from":0.9,"to":1,"cases":400,"covered":0.05,"stated":0.93,"observed":0.99,"interval":[0.95,1],"reviews":20,"considered":12,"state":"underconfident"}`
	return `{"org":"` + me + `","subject":"` + me + `","from":"2026-08-05T00:00:00Z","to":"2026-11-03T00:00:00Z","bins":[` +
		strings.Join(bins, ",") + `],"error":` + errorValue + `,"estimable":0.98,"added_later":true}`
}

func TestGetCalibration(t *testing.T) {
	from, to := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	c, calls := fakeAPI(t, 1, failOnce(calibrationReport("0.06"), http.StatusOK))
	got, err := c.GetCalibration(t.Context(), &oversight.CalibrationParams{Org: me, Subject: me, From: from, To: to.In(time.FixedZone("CET", 3600))})
	if err != nil {
		t.Fatal(err)
	}
	if got.Org != me || got.Subject != me || !got.From.Equal(from) || !got.To.Equal(to) || len(got.Bins) != oversight.CalibrationBinCount ||
		got.Error == nil || *got.Error != 0.06 || got.Estimable != 0.98 || !strings.Contains(string(got.RawJSON()), "added_later") {
		t.Fatalf("report %+v", got)
	}
	empty, few, calibrated, over, under := got.Bins[0], got.Bins[2], got.Bins[5], got.Bins[8], got.Bins[9]
	if empty.Cases != 0 || empty.Stated != nil || empty.Observed != nil || empty.State != oversight.CalibrationNotEstimable {
		t.Errorf("an empty bin %+v", empty)
	}
	if few.State != oversight.CalibrationNotEstimable || few.Stated == nil || *few.Stated != 0.24 || few.Observed != nil || few.Interval != nil ||
		few.Reviews != 6 || few.Considered != 4 {
		t.Errorf("a bin with too few checks %+v", few)
	}
	if calibrated.State != oversight.CalibrationCalibrated || calibrated.From != 0.5 || calibrated.To != 0.6 || calibrated.Cases != 40 || calibrated.Covered != 1 ||
		calibrated.Observed == nil || *calibrated.Observed != 0.6 || calibrated.Interval == nil || *calibrated.Interval != [2]float64{0.45, 0.73} || calibrated.Considered != 31 {
		t.Errorf("a calibrated bin %+v", calibrated)
	}
	if over.State != oversight.CalibrationOverconfident || over.Covered != 0.5 || under.State != oversight.CalibrationUnderconfident || under.Covered != 0.05 {
		t.Errorf("the other states %+v, %+v", over, under)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].method != http.MethodGet || cs[1].path != "/v1/oversight/calibration" || cs[1].query["org"] != me || cs[1].query["subject"] != me ||
		cs[1].query["from"] != "2026-08-05T00:00:00Z" || cs[1].query["to"] != "2026-11-03T00:00:00Z" || len(cs[1].query) != 4 {
		t.Errorf("calls %+v", cs)
	}

	// A report with no estimable bin has no error, which is not zero.
	none, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, http.StatusOK, calibrationReport("null"))
	})
	got, err = none.GetCalibration(t.Context(), &oversight.CalibrationParams{Org: me, Subject: me, From: from, To: to})
	if err != nil || got.Error != nil {
		t.Errorf("no error: %+v, %v", got, err)
	}
}

// BinOf finds the bin of a chance by the edges the report names: each bin
// holds its From and not its To, and the last bin holds 1.
func TestCalibrationBinOf(t *testing.T) {
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, http.StatusOK, calibrationReport("0.06"))
	})
	got, err := c.GetCalibration(t.Context(), &oversight.CalibrationParams{Org: me, Subject: me, From: time.Unix(0, 0), To: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[float64]float64{0: 0, 0.05: 0, 0.1: 0.1, 0.55: 0.5, 0.6: 0.6, 0.899: 0.8, 0.9: 0.9, 1: 0.9} {
		if b := got.BinOf(p); b == nil || b.From != want {
			t.Errorf("BinOf(%g) = %+v, want the bin from %g", p, b, want)
		}
	}
	if got.BinOf(0.55).State != oversight.CalibrationCalibrated {
		t.Error("the bin of 0.55 is not the calibrated one")
	}
	for _, p := range []float64{-0.01, 1.01} {
		if b := got.BinOf(p); b != nil {
			t.Errorf("BinOf(%g) = %+v, want none", p, b)
		}
	}
	if b := (&oversight.Calibration{}).BinOf(0.5); b != nil {
		t.Errorf("a report with no bins: %+v", b)
	}
}

// An explanation reads the state of its recorded prediction's bin, and is
// empty without one.
func TestAnExplanationReadsItsCalibration(t *testing.T) {
	for body, want := range map[string]oversight.CalibrationState{
		strings.Replace(explanation, `"verdict":"ask",`, `"verdict":"ask","calibration":"overconfident",`, 1): oversight.CalibrationOverconfident,
		strings.Replace(explanation, `"verdict":"ask",`, `"verdict":"ask","calibration":"calibrated",`, 1):    oversight.CalibrationCalibrated,
		explanation: "",
	} {
		c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, body) })
		got, err := c.GetExplanation(t.Context(), &oversight.ExplanationParams{Org: me, Subject: me, ActionID: action})
		if err != nil || got.Calibration != want {
			t.Errorf("calibration %q, want %q: %v", got.Calibration, want, err)
		}
	}
}

// A caller that may not read the person's reports, and a window the API
// refuses, come back as the API's errors; a window with no bounds, or
// bounds out of order, and a call with no person, are refused before any
// request.
func TestGetCalibrationRefusals(t *testing.T) {
	from, to := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	no, noCalls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusForbidden, "forbidden") })
	if _, err := no.GetCalibration(t.Context(), &oversight.CalibrationParams{Org: me, Subject: "https://auth.example|usr_8", From: from, To: to}); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("another person: %v", err)
	}
	bad, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusBadRequest, "invalid_query") })
	_, err := bad.GetCalibration(t.Context(), &oversight.CalibrationParams{Org: me, Subject: me, From: from, To: to})
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || apiErr.Code != "invalid_query" {
		t.Errorf("a window the API refuses: %v", err)
	}
	n := len(noCalls())
	for _, p := range []*oversight.CalibrationParams{nil, {Subject: me, From: from, To: to}, {Org: me, From: from, To: to},
		{Org: me, Subject: me, To: to}, {Org: me, Subject: me, From: from}, {Org: me, Subject: me, From: to, To: from}, {Org: me, Subject: me, From: from, To: from}} {
		if _, err := no.GetCalibration(t.Context(), p); err == nil || !strings.HasPrefix(err.Error(), "oversight: ") {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if len(noCalls()) != n {
		t.Error("a call refused before any request reached the API")
	}
	undecodable, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, `[]`) })
	if _, err := undecodable.GetCalibration(t.Context(), &oversight.CalibrationParams{Org: me, Subject: me, From: from, To: to}); err == nil {
		t.Error("a list decoded as a report")
	}
}
