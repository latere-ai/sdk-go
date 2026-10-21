// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"time"
)

// jwt is a token of the claims, with a header and a signature that are
// never read.
func jwt(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc([]byte(claims)) + "." + enc([]byte("not a signature"))
}

func TestReadUnverifiedClaims(t *testing.T) {
	for name, c := range map[string]struct {
		token string
		want  UnverifiedClaims
	}{
		"one audience, in an organization": {
			jwt(`{"aud":"api.latere.ai","sub":"8f1c","exp":1792411200,"org_id":"org_01J9ZK3M","iss":"https://auth.latere.ai"}`),
			UnverifiedClaims{Audiences: []string{"api.latere.ai"}, Subject: "8f1c", Expiry: time.Unix(1792411200, 0), OrgID: "org_01J9ZK3M"},
		},
		"a list of audiences, in a person's own context": {
			jwt(`{"aud":["api.latere.ai","cella"],"sub":"8f1c","exp":1792411200.5}`),
			UnverifiedClaims{Audiences: []string{"api.latere.ai", "cella"}, Subject: "8f1c", Expiry: time.Unix(1792411200, 5e8)},
		},
		"no claims of these": {jwt(`{}`), UnverifiedClaims{}},
		"null claims":        {jwt(`{"aud":null,"sub":null,"exp":null,"org_id":null}`), UnverifiedClaims{}},
		"a padded middle": {
			"h." + base64.URLEncoding.EncodeToString([]byte(`{"sub":"a"}`)) + ".s",
			UnverifiedClaims{Subject: "a"},
		},
	} {
		got, err := ReadUnverifiedClaims(c.token)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !slices.Equal(got.Audiences, c.want.Audiences) || got.Subject != c.want.Subject || !got.Expiry.Equal(c.want.Expiry) || got.OrgID != c.want.OrgID {
			t.Errorf("%s: read %+v, want %+v", name, got, c.want)
		}
	}
}

func TestReadUnverifiedClaimsRefuses(t *testing.T) {
	const secret = "s3cr3t-claim-value"
	for name, token := range map[string]string{
		"no token":                "",
		"two parts":               "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"`+secret+`"}`)),
		"a key, not a token":      "lat_" + secret,
		"a middle not base64url":  "a.!!" + secret + ".c",
		"claims not JSON":         jwt(secret),
		"claims not an object":    jwt(`["` + secret + `"]`),
		"an audience of a number": jwt(`{"aud":42,"sub":"` + secret + `"}`),
		"a subject not a string":  jwt(`{"sub":{"id":"` + secret + `"}}`),
		"an org not a string":     jwt(`{"org_id":7,"sub":"` + secret + `"}`),
		"an expiry not a number":  jwt(`{"exp":"` + secret + `"}`),
		"an expiry out of range":  jwt(`{"exp":1e300}`),
	} {
		_, err := ReadUnverifiedClaims(token)
		if err == nil {
			t.Errorf("%s was read", name)
			continue
		}
		if strings.Contains(err.Error(), secret) || token != "" && strings.Contains(err.Error(), token) {
			t.Errorf("%s: the error quotes the token: %v", name, err)
		}
	}
}
