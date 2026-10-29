// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package attachment_test

import (
	"encoding/json"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/attachment"
)

func TestDigest(t *testing.T) {
	// The SHA-256 of "abc", FIPS 180-2's first example.
	const abc = "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := attachment.Digest([]byte("abc")); got != abc {
		t.Fatal(got)
	}
	h := attachment.NewHash()
	h.Write([]byte("ab"))
	h.Write([]byte("c"))
	if got := attachment.Sum(h.Sum(nil)); got != abc {
		t.Fatal(got)
	}
	if sum, err := attachment.ParseBlob(abc); err != nil || len(sum) != 32 {
		t.Fatal(sum, err)
	}
	for _, bad := range []string{"", "ba7816bf", "sha256:", "sha256:zz", "md5:" + abc[7:], strings.ToUpper(abc), abc + "00"} {
		if _, err := attachment.ParseBlob(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := attachment.Attachment{Name: "Plan 2027.pdf", MediaType: "application/pdf", Size: 3, Blob: attachment.Digest([]byte("abc"))}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\nb", "a\x00b", "\xff", strings.Repeat("x", attachment.MaxNameBytes+1)} {
		a := ok
		a.Name = name
		if err := a.Validate(); err == nil {
			t.Errorf("name %q valid", name)
		}
	}
	a := ok
	a.Size = -1
	if err := a.Validate(); err == nil {
		t.Error("a negative size is valid")
	}
}

// TestWire holds the field names to the ones the agents API sends for a
// session's attachment, which this type is the common part of.
func TestWire(t *testing.T) {
	b, err := json.Marshal(attachment.Attachment{Name: "a.png", MediaType: "image/png", Size: 1, Blob: "sha256:00", Ref: "attachments/m1/a.png",
		Preview: &attachment.Preview{MediaType: "image/jpeg", Width: 2, Height: 3, DurationMillis: 4, Waveform: []byte{0, 255}, Pages: 5}})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"name":"a.png","media_type":"image/png","size":1,"blob":"sha256:00","ref":"attachments/m1/a.png",` +
		`"preview":{"media_type":"image/jpeg","width":2,"height":3,"duration_ms":4,"waveform":"AP8=","pages":5}}`
	if string(b) != want {
		t.Fatalf("%s\nwant %s", b, want)
	}
}
