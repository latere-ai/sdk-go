// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

// UnverifiedClaims are what a token the platform issued says about itself,
// as ReadUnverifiedClaims reads them: nothing in them is checked, neither
// the token's signature, nor its issuer, nor its audience, nor its expiry.
// They are as true as the source the token came from.
type UnverifiedClaims struct {
	// Audiences are the services the token is addressed to, its "aud",
	// such as DefaultAudience; empty when it names none.
	Audiences []string
	// Subject is who the token speaks for, its "sub".
	Subject string
	// Expiry is when the token says it stops working, its "exp"; zero
	// when it names none.
	Expiry time.Time
	// OrgID is the organization whose context the token acts in, its
	// "org_id"; empty for a token in a person's own context.
	OrgID string
}

// errNotAToken is the error of a value that is not a JSON Web Token.
var errNotAToken = errors.New("latere: ReadUnverifiedClaims: the token is not a JWT of three parts whose middle one is base64url JSON")

// ReadUnverifiedClaims reads the claims of a token the platform issued, a
// JSON Web Token, without verifying it: it decodes the token's middle part
// and checks nothing, so a forged token reads as well as a real one. It is
// for a program that holds a token from a source it already trusts and
// presents it to the API, which verifies it, and needs to know before
// that whom the token names, the context it acts in, or when to ask for a
// new one, such as a token another program on the same computer handed
// over. Never decide whether to trust a token by what it returns; present
// the token to the API, which refuses one it did not issue with 401.
//
// "aud" may be one string or a list of them, and "exp" is in seconds. It
// returns an error for a value that is not such a token, or whose claims
// have another type; the error never quotes the token or a claim.
func ReadUnverifiedClaims(token string) (UnverifiedClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return UnverifiedClaims{}, errNotAToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return UnverifiedClaims{}, errNotAToken
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil || claims == nil {
		return UnverifiedClaims{}, errNotAToken
	}
	var out UnverifiedClaims
	if raw, ok := claims["aud"]; ok && string(raw) != "null" {
		var one string
		if json.Unmarshal(raw, &one) == nil {
			out.Audiences = []string{one}
		} else if json.Unmarshal(raw, &out.Audiences) != nil {
			return UnverifiedClaims{}, errors.New(`latere: ReadUnverifiedClaims: the token's "aud" is neither a string nor a list of strings`)
		}
	}
	for name, into := range map[string]*string{"sub": &out.Subject, "org_id": &out.OrgID} {
		if raw, ok := claims[name]; ok && string(raw) != "null" && json.Unmarshal(raw, into) != nil {
			return UnverifiedClaims{}, errors.New(`latere: ReadUnverifiedClaims: the token's "` + name + `" is not a string`)
		}
	}
	if raw, ok := claims["exp"]; ok && string(raw) != "null" {
		// Seconds past 2^53 hold no exact value, and lie far past any
		// expiry a token is issued with.
		var secs float64
		if json.Unmarshal(raw, &secs) != nil || math.Abs(secs) > 1<<53 {
			return UnverifiedClaims{}, errors.New(`latere: ReadUnverifiedClaims: the token's "exp" is not a number of seconds`)
		}
		if secs > 0 {
			whole, frac := math.Modf(secs)
			out.Expiry = time.Unix(int64(whole), int64(frac*1e9))
		}
	}
	return out, nil
}
