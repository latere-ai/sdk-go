// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

func TestGetDataPlaces(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		answerJSON(w, http.StatusOK, `{"sessions":{"country":"DE","place":"Frankfurt"}}`)
	})
	d, err := c.GetDataPlaces(t.Context())
	if err != nil || d.Sessions == nil || d.Sessions.Country != "DE" || d.Sessions.Place != "Frankfurt" || d.Machines != nil || d.RawJSON() == nil {
		t.Errorf("GetDataPlaces: %+v, %v; want sessions stated and machines not", d, err)
	}
	if got := calls()[0]; got.Method != http.MethodGet || got.Path != "/v1/account/data" {
		t.Errorf("request: %+v", got)
	}
}

func TestGetModelDataTerms(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		answerJSON(w, http.StatusOK, `{"models":[{"name":"vendor/model-a","display_name":"Model A","retention":"provider","omitted":1,
			"price":{"currency":"USD","per":1000000,"input":"0.4","output":"1.6"},
			"served_by":[{"provider":"Amazon Bedrock","retention":"none","countries":["US"]}]},{"name":"vendor/model-b"}]}`)
	})
	terms, err := c.GetModelDataTerms(t.Context(), "vendor/model-a", "vendor/model-b")
	if err != nil || len(terms.Models) != 2 {
		t.Fatalf("GetModelDataTerms: %+v, %v", terms, err)
	}
	a, b := terms.Models[0], terms.Models[1]
	if a.DisplayName != "Model A" || a.Retention != "provider" || a.Omitted != 1 || a.Price.Output != "1.6" ||
		a.ServedBy[0].Provider != "Amazon Bedrock" || a.ServedBy[0].Countries[0] != "US" || b.Name != "vendor/model-b" || b.Price != nil {
		t.Errorf("terms: %+v", terms.Models)
	}
	if got := calls()[0]; got.Method != http.MethodGet || got.Path != "/v1/account/data/models" || got.Query != "name=vendor%2Fmodel-a&name=vendor%2Fmodel-b" {
		t.Errorf("request: %+v", got)
	}

	tooMany := make([]string, account.MaxDataTermsModels+1)
	for i := range tooMany {
		tooMany[i] = "vendor/m" + strconv.Itoa(i)
	}
	for name, names := range map[string][]string{"none": nil, "too many": tooMany, "an empty name": {"vendor/a", ""}} {
		if _, err := c.GetModelDataTerms(t.Context(), names...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := c.GetModelDataTerms(t.Context(), tooMany[:account.MaxDataTermsModels]...); err != nil {
		t.Errorf("the most models a read names: %v", err)
	}
	if n := len(calls()); n != 2 {
		t.Errorf("sent %d requests; a read past its bounds was sent", n)
	}
	// The API counts distinct names, so a name given twice is sent once.
	twice := append(tooMany[:account.MaxDataTermsModels:account.MaxDataTermsModels], tooMany[0])
	if _, err := c.GetModelDataTerms(t.Context(), twice...); err != nil {
		t.Errorf("the most distinct models, one named twice: %v", err)
	}
	if q := calls()[2].Query; strings.Count(q, "name=vendor%2Fm0&") != 1 || strings.Count(q, "name=") != account.MaxDataTermsModels {
		t.Errorf("sent the query %q", q)
	}
}

// A checkout is sent once: the API takes no key, and a repeat would open a
// second payment page.
func TestStartCheckout(t *testing.T) {
	c, calls := capturingAPI(t, 3, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			answerUnavailable(w)
			return
		}
		answerJSON(w, http.StatusOK, `{"url":"https://pay.example/c/cs_1"}`)
	})
	params := &account.StartCheckoutParams{AmountMicroUSD: 10_000_000, SuccessURL: "https://chat.latere.ai/?credit=added", CancelURL: "https://chat.latere.ai/"}
	var apiErr *latere.Error
	if _, err := c.StartCheckout(t.Context(), params); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a checkout refused with 503: %v", err)
	}
	if n := len(calls()); n != 1 {
		t.Fatalf("sent %d times, want once", n)
	}
	page, err := c.StartCheckout(t.Context(), params)
	if err != nil || page.URL != "https://pay.example/c/cs_1" || page.RawJSON() == nil {
		t.Fatalf("StartCheckout: %+v, %v", page, err)
	}
	got := calls()[1]
	if got.Method != http.MethodPost || got.Path != "/v1/account/wallet/checkout" ||
		got.RawBody != `{"amount_micro":10000000,"success_url":"https://chat.latere.ai/?credit=added","cancel_url":"https://chat.latere.ai/"}` {
		t.Errorf("request: %+v", got)
	}
	for name, p := range map[string]*account.StartCheckoutParams{
		"nil":       nil,
		"no amount": {SuccessURL: "https://a/", CancelURL: "https://a/"},
		"no return": {AmountMicroUSD: 1, CancelURL: "https://a/"},
		"no cancel": {AmountMicroUSD: 1, SuccessURL: "https://a/"},
	} {
		if _, err := c.StartCheckout(t.Context(), p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if n := len(calls()); n != 2 {
		t.Errorf("an invalid checkout was sent")
	}
}

func TestRecordArrival(t *testing.T) {
	c, calls := capturingAPI(t, 1, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			answerUnavailable(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.RecordArrival(t.Context(), &account.RecordArrivalParams{SourceHost: "news.ycombinator.com"}); err != nil {
		t.Fatal(err)
	}
	sent := calls()
	if len(sent) != 2 || sent[1].Method != http.MethodPut || sent[1].Path != "/v1/account/arrival" || sent[1].RawBody != `{"source_host":"news.ycombinator.com"}` {
		t.Errorf("RecordArrival sent %+v; want a PUT, retried, with the source alone", sent)
	}
	for _, p := range []*account.RecordArrivalParams{nil, {}} {
		if err := c.RecordArrival(t.Context(), p); err == nil || !strings.Contains(err.Error(), "neither") {
			t.Errorf("RecordArrival(%+v): %v", p, err)
		}
	}
}
