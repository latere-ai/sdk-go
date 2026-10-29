// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"latere.ai/x/sdk-go/mls"
)

func TestStateRoundTrip(t *testing.T) {
	s := State{Kind: KindGroup, Level: LevelReadsAlong, People: []string{"a", "b", "c"}, Owners: []string{"a", "c"}}
	ext, err := s.Extension()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseState([]mls.Extension{{Type: mls.ExtensionExternalSenders}, ext})
	if err != nil || !got.Equal(s) {
		t.Fatalf("%+v, %v", got, err)
	}
	// The pinned encoding: version, kind, level, people, owners by index.
	want := "01020100030161016201630002" + "0000" + "0002"
	if fmt.Sprintf("%x", ext.Data) != want {
		t.Fatalf("encoding %x, want %s", ext.Data, want)
	}
}

func TestStateValidation(t *testing.T) {
	for _, s := range []State{
		{Kind: 0, People: []string{"a"}, Owners: []string{"a"}},
		{Kind: KindGroup, Level: 3, People: []string{"a"}, Owners: []string{"a"}},
		{Kind: KindGroup, Owners: []string{"a"}},
		{Kind: KindGroup, People: []string{"a", "a"}, Owners: []string{"a"}},
		{Kind: KindGroup, People: []string{"a"}},
		{Kind: KindGroup, People: []string{"a"}, Owners: []string{"b"}},
		{Kind: KindGroup, People: []string{"a", "b"}, Owners: []string{"b", "a"}},
		{Kind: KindGroup, People: []string{"a", "b"}, Owners: []string{"a", "a"}},
		{Kind: KindOneOnOne, People: []string{"a", "b", "c"}, Owners: []string{"a"}},
		{Kind: KindGroup, People: []string{"a/b"}, Owners: []string{"a/b"}},
	} {
		if _, err := s.Extension(); err == nil {
			t.Errorf("%+v encoded", s)
		}
	}
}

func TestParseStateRefuses(t *testing.T) {
	good, err := State{Kind: KindGroup, People: []string{"a", "b"}, Owners: []string{"a"}}.Extension()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseState(nil); err == nil {
		t.Error("no state parsed")
	}
	if _, err := ParseState([]mls.Extension{good, good}); err == nil {
		t.Error("two states parsed")
	}
	for _, data := range [][]byte{
		append(bytes.Clone(good.Data), 0),                 // trailing
		good.Data[:len(good.Data)-1],                      // truncated
		append([]byte{2}, good.Data[1:]...),               // version
		{1, 2, 0, 0, 1, 1, 'a', 0, 1, 0, 5},               // owner index out of range
		{1, 2, 0, 0, 1, 0xff, 'a', 0, 1, 0, 0},            // person past the end
		{1, 2, 0, 0, 2, 1, 'a', 1, 'b', 0, 2, 0, 1, 0, 0}, // owners out of order
	} {
		if _, err := ParseState([]mls.Extension{{Type: StateExtension, Data: data}}); err == nil {
			t.Errorf("%x parsed", data)
		}
	}
}

func TestNamesAsText(t *testing.T) {
	b, err := json.Marshal(struct {
		K Kind
		L Level
		R RecordKind
	}{KindOneOnOne, LevelTakesPart, RecordRecovery})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"K":"one_on_one","L":"takes_part","R":"recovery"}` {
		t.Fatal(string(b))
	}
	var v struct {
		K Kind
		L Level
		R RecordKind
	}
	if err := json.Unmarshal(b, &v); err != nil || v.K != KindOneOnOne || v.L != LevelTakesPart || v.R != RecordRecovery {
		t.Fatalf("%+v, %v", v, err)
	}
	for _, bad := range []string{`{"K":"x"}`, `{"L":"x"}`, `{"R":"x"}`} {
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s decoded", bad)
		}
	}
	if _, err := Kind(9).MarshalText(); err == nil {
		t.Error("kind 9 encoded")
	}
	if _, err := Level(9).MarshalText(); err == nil {
		t.Error("level 9 encoded")
	}
}
