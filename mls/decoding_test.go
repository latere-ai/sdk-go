// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"testing"
)

// Each format refuses a value its encoder would not write.
func TestDecodingRefusals(t *testing.T) {
	cred := func(typ uint16) []byte { var w writer; w.u16(typ); return w.b }
	cases := map[string]struct {
		in []byte
		f  func(*reader)
	}{
		"a credential of an unknown type":         {cred(0x7777), func(r *reader) { var c Credential; c.unmarshal(r) }},
		"a sender of an unknown type":             {[]byte{9}, func(r *reader) { var s sender; s.unmarshal(r) }},
		"a proposal of an unknown type":           {cred(0x7777), func(r *reader) { var p proposal; p.unmarshal(r) }},
		"a proposal-or-reference of unknown kind": {[]byte{9}, func(r *reader) { var p proposalOrRef; p.unmarshal(r) }},
		"a PSK of an unknown type":                {[]byte{9}, func(r *reader) { var p preSharedKeyID; p.unmarshal(r) }},
		"a resumption PSK of an unknown usage":    {[]byte{uint8(pskResumption), 9}, func(r *reader) { var p preSharedKeyID; p.unmarshal(r) }},
		"content of an unknown type": {[]byte{9}, func(r *reader) {
			c := framedContent{contentType: 9}
			c.unmarshalBody(r)
		}},
		"a tree of no nodes":         {[]byte{0}, func(r *reader) { unmarshalTree(r) }},
		"a tree ending in a blank":   {[]byte{2, 0, 0}, func(r *reader) { unmarshalTree(r) }},
		"a tree of an unknown node":  {[]byte{2, 1, 9}, func(r *reader) { unmarshalTree(r) }},
		"a parent where a leaf sits": {[]byte{2, 1, nodeTypeParent}, func(r *reader) { unmarshalTree(r) }},
		"a leaf where a parent sits": {[]byte{4, 0, 1, nodeTypeLeaf}, func(r *reader) { unmarshalTree(r) }},
	}
	for name, c := range cases {
		if err := decode(c.in, c.f); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestX509CredentialRoundTrip(t *testing.T) {
	c := Credential{Type: CredentialX509, Certificates: [][]byte{[]byte("leaf"), []byte("root")}}
	var back Credential
	if err := decode(encode(c.marshal), back.unmarshal); err != nil {
		t.Fatal(err)
	}
	if !back.equal(c) || len(back.Certificates) != 2 {
		t.Errorf("round trip gave %+v", back)
	}
	cp := c.clone()
	cp.Certificates[0][0] = 'X'
	if c.Certificates[0][0] != 'l' {
		t.Error("clone shares the certificates")
	}
	if !bytes.Equal(back.Certificates[1], []byte("root")) {
		t.Error("the chain lost its order")
	}
}

// A saved group that is not whole or not consistent is not restored.
func TestUnmarshalGroupRefusals(t *testing.T) {
	alice, _ := pair(t, Config{})
	b, err := alice.group.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalGroup(b, Config{}); err != nil {
		t.Fatal(err)
	}
	status := bytes.Clone(b)
	status[1] = 99
	if _, err := UnmarshalGroup(status, Config{}); err == nil {
		t.Error("a group of an unknown status was restored")
	}
	suiteID := bytes.Clone(b)
	suiteID[2], suiteID[3] = 0x77, 0x77
	if _, err := UnmarshalGroup(suiteID, Config{}); err == nil {
		t.Error("a group of an unknown suite was restored")
	}
	if _, err := UnmarshalGroup(b[:len(b)/2], Config{}); err == nil {
		t.Error("half a group was restored")
	}
	if _, err := UnmarshalGroup(append(bytes.Clone(b), 0), Config{}); err == nil {
		t.Error("a group with a trailing byte was restored")
	}
}
