// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The fixed numbers of a calibration report.
const (
	// CalibrationBinCount is how many bins a report has: [0, 0.1),
	// [0.1, 0.2), and so on to [0.9, 1], the last one with 1 included.
	CalibrationBinCount = 10
	// CalibrationMinEffectiveSize is the smallest Kish effective size of a
	// bin's reviews that gives an observed rate; a bin below it is
	// CalibrationNotEstimable.
	CalibrationMinEffectiveSize = 20
	// CalibrationWindow is the window Explanation.Calibration reads: the
	// last 90 days.
	CalibrationWindow = 90 * 24 * time.Hour
)

// CalibrationState says how the chances the decision service stated in a
// bin compare with the rate the person's checks measured.
type CalibrationState string

// The states of a bin.
const (
	// CalibrationCalibrated is a bin whose Stated is in the Interval of
	// Observed, the edges included.
	CalibrationCalibrated CalibrationState = "calibrated"
	// CalibrationOverconfident is a bin whose Stated is outside the
	// Interval and further from 0.5 than Observed: the decision service
	// was surer than the checks show.
	CalibrationOverconfident CalibrationState = "overconfident"
	// CalibrationUnderconfident is a bin whose Stated is outside the
	// Interval and not further from 0.5 than Observed: the decision service
	// was less sure than the checks show.
	CalibrationUnderconfident CalibrationState = "underconfident"
	// CalibrationNotEstimable is a bin whose reviews have a Kish effective
	// size below CalibrationMinEffectiveSize: too few checks to tell, so
	// its Observed is nil.
	CalibrationNotEstimable CalibrationState = "not_estimable"
)

// CalibrationParams name whose predictions GetCalibration reads, over
// which window.
type CalibrationParams struct {
	// Org names the context and Subject the person, as the package
	// documentation says. Both are required.
	Org, Subject string
	// From and To bound the window: the steps the decision service first
	// received from From, included, to To. From must be before To. Both
	// are required.
	From, To time.Time
}

// Calibration is a person's calibration report: the chances of allowing
// that the decision service stated for the person's steps over one
// window, beside the rate of allowing that the person's checks measured,
// in CalibrationBinCount bins of the stated chance.
//
// A client shows a chance of allowing, as a number or in words that state
// a probability such as "likely", only where two conditions are true: the
// bin of the step's recorded prediction is CalibrationCalibrated in the
// person's report for the last CalibrationWindow, which
// Explanation.Calibration gives; and, for a chance above the trust
// profile's allow threshold, the Certificate of OfLearned holds. In all
// other cases it states no probability.
type Calibration struct {
	Org     string    `json:"org"`
	Subject string    `json:"subject"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	// Bins are the CalibrationBinCount bins, the lowest first.
	Bins []CalibrationBin `json:"bins"`
	// Error is the mean distance between Stated and Observed over the
	// bins that are not CalibrationNotEstimable, each weighted by its
	// Cases; nil when no bin is estimable, which is not zero.
	Error *float64 `json:"error"`
	// Estimable is the share, from 0 to 1, of the report's steps that are
	// in a bin that is not CalibrationNotEstimable; 0 with no step.
	Estimable float64 `json:"estimable"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the report, with every
// field, including those this version of the package does not model.
func (c *Calibration) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the report and keeps the JSON for RawJSON.
func (c *Calibration) UnmarshalJSON(b []byte) error {
	type plain Calibration
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// BinOf returns the bin of the report whose range holds the chance p: the
// bin from From, included, to To, and the last bin with 1 included. It
// returns nil for a p outside 0 to 1, or for a report with no such bin.
func (c *Calibration) BinOf(p float64) *CalibrationBin {
	if p < 0 || p > 1 {
		return nil
	}
	for i := range c.Bins {
		b := &c.Bins[i]
		if p >= b.From && (p < b.To || i == len(c.Bins)-1 && p <= b.To) {
			return b
		}
	}
	return nil
}

// CalibrationBin is one bin of a calibration report: the steps whose
// stated chance of allowing is from From up to To. A bin below 0.5
// estimates the share of allows, and a bin of 0.5 or more estimates the
// share of denials and observes one minus it, so the Observed of a bin
// above the allow threshold is one minus the false-allow rate of its
// allows.
type CalibrationBin struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
	// Cases counts the steps in the bin.
	Cases int `json:"cases"`
	// Covered is the share, from 0 to 1, of the Cases whose decision had
	// a review probability above zero: the steps the person could be
	// shown to check. 0 with no case.
	Covered float64 `json:"covered"`
	// Stated is the mean chance of allowing the decision service stated
	// for the covered steps; nil when the bin has none.
	Stated *float64 `json:"stated"`
	// Observed is the rate of allowing the person's checks of the covered
	// steps measured, each check weighted by one over its review
	// probability; nil when the bin is CalibrationNotEstimable, which is
	// not zero.
	Observed *float64 `json:"observed"`
	// Interval is the Wilson 95% interval of Observed at the Kish
	// effective size of the reviews, present with Observed.
	Interval *[2]float64 `json:"interval,omitempty"`
	// Reviews counts the checks of the covered steps, and Considered
	// those among them that are considered answers, whose weight is 1. No
	// number of the bin reads the weight.
	Reviews    int `json:"reviews"`
	Considered int `json:"considered"`
	// State is how Stated compares with Observed.
	State CalibrationState `json:"state"`
}

// GetCalibration reads the calibration report of the person params names
// over the window. It refuses a caller that may not read the person's
// reports with an error matching latere.ErrForbidden, and a window the API
// cannot read with a 400 *latere.Error whose Code is "invalid_query".
func (c *Client) GetCalibration(ctx context.Context, params *CalibrationParams) (*Calibration, error) {
	if params == nil || params.Org == "" || params.Subject == "" {
		return nil, errors.New("oversight: GetCalibration needs the Org and the Subject")
	}
	if params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("oversight: GetCalibration needs a window whose From is before its To")
	}
	q := url.Values{
		"org": {params.Org}, "subject": {params.Subject},
		"from": {params.From.UTC().Format(time.RFC3339Nano)}, "to": {params.To.UTC().Format(time.RFC3339Nano)},
	}
	var out Calibration
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/calibration", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
