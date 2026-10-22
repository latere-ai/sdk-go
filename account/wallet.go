// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"latere.ai/x/sdk-go/latere"
)

// StartCheckoutParams are what a payment page for credit is opened with.
type StartCheckoutParams struct {
	// AmountMicroUSD is what the person pays, in millionths of a US
	// dollar. The API rounds it down to whole cents, and refuses one under
	// the wallet's TopUp.MinMicroUSD with 400 and the code "below_min".
	AmountMicroUSD int64 `json:"amount_micro"`
	// SuccessURL is where the payment page sends the person once they
	// paid: an absolute address, of at most 2048 bytes, on the origin of a
	// Latere product.
	SuccessURL string `json:"success_url"`
	// CancelURL is where it sends them when they leave without paying, on
	// the origin of a Latere product too.
	CancelURL string `json:"cancel_url"`
}

// Checkout is a payment page for credit.
type Checkout struct {
	// URL is the payment page, which the program sends the person to.
	URL string `json:"url"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the checkout, with every
// field, including those this package does not model; nil for a Checkout
// the program built itself.
func (c *Checkout) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the checkout and keeps the JSON for RawJSON.
func (c *Checkout) UnmarshalJSON(b []byte) error {
	type plain Checkout
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// StartCheckout opens a payment page for credit in the wallet of the
// token's context and returns its address: the person's own wallet in
// their personal context, an organization's, which its owners and admins
// add to, in an organization's. What the payment adds is fixed when the
// page opens, under the spread GetWallet's TopUp states then. The credit
// arrives once the payment processor confirms the payment, usually within
// a minute of the person's return to SuccessURL, and each payment adds
// credit once.
//
// The call is sent once and never retried: the API takes no idempotency
// key here, and a repeat opens a second page. A page left unpaid adds
// nothing, so a caller whose call failed may start another.
//
// The API refuses a return address it does not send a person back to with
// 400 and the code "return_not_allowed", a member of an organization who
// is not its owner or admin with 403, a deployment that takes no payments
// with 501 and "payments_unconfigured", and one that keeps no wallets with
// 503 and "wallet_unconfigured".
func (c *Client) StartCheckout(ctx context.Context, params *StartCheckoutParams) (*Checkout, error) {
	if params == nil || params.AmountMicroUSD <= 0 {
		return nil, errors.New("account: StartCheckout: the amount is not positive")
	}
	if params.SuccessURL == "" || params.CancelURL == "" {
		return nil, errors.New("account: StartCheckout: SuccessURL and CancelURL are both needed")
	}
	var out Checkout
	req := &latere.Request{Method: http.MethodPost, Path: "/v1/account/wallet/checkout", Body: params, Retry: latere.RetryNever}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
