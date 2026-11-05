// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestHistoryRangeValidate(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	good := []HistoryRange{
		{Kind: RangeNone}, {Kind: RangeAll}, {Kind: RangeSinceTime, Since: at}, {Kind: RangeSinceMessage, SinceMessage: "m1"},
	}
	for _, h := range good {
		if err := h.validate(); err != nil {
			t.Errorf("%+v: %v", h, err)
		}
	}
	bad := map[string]HistoryRange{
		"none with a time":          {Kind: RangeNone, Since: at},
		"all with a message":        {Kind: RangeAll, SinceMessage: "m1"},
		"since a time without it":   {Kind: RangeSinceTime},
		"since a time and message":  {Kind: RangeSinceTime, Since: at, SinceMessage: "m1"},
		"since a message with time": {Kind: RangeSinceMessage, Since: at, SinceMessage: "m1"},
		"since a message, no id":    {Kind: RangeSinceMessage},
		"an unknown kind":           {Kind: 200},
	}
	for name, h := range bad {
		if err := h.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHistoryRangeFilter(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	msgs := []*SharedMessage{
		{Payload: Payload{ID: "m1", SentAt: at.Add(-time.Hour)}},
		{Payload: Payload{ID: "m2", SentAt: at}},
		{Payload: Payload{ID: "m3", SentAt: at.Add(time.Hour)}},
	}
	for _, c := range []struct {
		h    HistoryRange
		kept int
	}{
		{HistoryRange{Kind: RangeNone}, 0},
		{HistoryRange{Kind: RangeAll}, 3},
		{HistoryRange{Kind: RangeSinceTime, Since: at}, 2},
		{HistoryRange{Kind: RangeSinceMessage, SinceMessage: "m2"}, 2},
		{HistoryRange{Kind: RangeSinceMessage, SinceMessage: "nope"}, 0},
	} {
		kept, dropped := c.h.Filter(msgs)
		if len(kept) != c.kept || dropped != 3-c.kept {
			t.Errorf("%+v kept %d and dropped %d, want %d kept", c.h, len(kept), dropped, c.kept)
		}
	}
}

func TestHistoryRangeDecoding(t *testing.T) {
	var w writer
	HistoryRange{Kind: RangeAll}.marshal(&w)
	var h HistoryRange
	r := &reader{b: w.b}
	h.unmarshal(r)
	if r.err != nil || h.Kind != RangeAll || !h.Since.IsZero() {
		t.Errorf("decoded %+v, %v", h, r.err)
	}
	// The zero time is written as 0, so a start of the zero time in
	// milliseconds cannot be a second encoding of "no start".
	zero := time.Time{}.UnixMilli()
	w = writer{}
	w.u8(uint8(RangeSinceTime))
	w.u64(uint64(zero))
	w.vec8(nil)
	r = &reader{b: w.b}
	h = HistoryRange{}
	h.unmarshal(r)
	if r.err == nil {
		t.Errorf("a start of the zero time decoded as %+v", h)
	}
}

func TestJoinerNoticeValidate(t *testing.T) {
	good := JoinerNotice{
		Range: HistoryRange{Kind: RangeAll}, Owner: "alice", Ref: Folder + "/bundle",
		Digest: make([]byte, sha256.Size), Size: 10, Key: make([]byte, BundleKeySize),
	}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*JoinerNotice){
		"a bad range":       func(j *JoinerNotice) { j.Range = HistoryRange{Kind: 200} },
		"no owner":          func(j *JoinerNotice) { j.Owner = "" },
		"a short digest":    func(j *JoinerNotice) { j.Digest = j.Digest[:5] },
		"a short key":       func(j *JoinerNotice) { j.Key = j.Key[:5] },
		"no size":           func(j *JoinerNotice) { j.Size = 0 },
		"a size over limit": func(j *JoinerNotice) { j.Size = MaxBundleBytes + 1 },
	} {
		j := good
		mutate(&j)
		if err := j.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	none := JoinerNotice{Range: HistoryRange{Kind: RangeNone}}
	if err := none.validate(); err != nil {
		t.Errorf("a notice that shares nothing: %v", err)
	}
	none.Owner = "alice"
	if err := none.validate(); err == nil {
		t.Error("a notice that shares nothing but names a bundle was accepted")
	}
}

func notice(version uint8, commitment []byte, parts ...[2][]byte) []byte {
	var w writer
	w.u8(version)
	w.vec8(commitment)
	w.vec32([]byte("members"))
	w.u16(uint16(len(parts)))
	for _, p := range parts {
		w.vec32(p[0])
		w.vec32(p[1])
	}
	return w.b
}

func TestParseCommitNoticeRefusals(t *testing.T) {
	c := make([]byte, sha256.Size)
	if _, err := ParseCommitNotice(notice(noticeVersion, c, [2][]byte{[]byte("k"), []byte("s")})); err != nil {
		t.Fatalf("a well-formed notice: %v", err)
	}
	if _, err := ParseCommitNotice(notice(noticeVersion+1, c)); !errors.Is(err, errFormat) {
		t.Errorf("another version: %v", err)
	}
	if _, err := ParseCommitNotice(notice(noticeVersion, c[:5])); !errors.Is(err, errFormat) {
		t.Errorf("a short commitment: %v", err)
	}
	two := [2][]byte{[]byte("k"), []byte("s")}
	if _, err := ParseCommitNotice(notice(noticeVersion, c, two, two)); !errors.Is(err, errFormat) {
		t.Errorf("two parts for one leaf: %v", err)
	}
	whole := notice(noticeVersion, c, two)
	if _, err := ParseCommitNotice(append(bytes.Clone(whole), 0)); !errors.Is(err, errFormat) {
		t.Errorf("trailing bytes: %v", err)
	}
	if _, err := ParseCommitNotice(whole[:len(whole)-3]); !errors.Is(err, errFormat) {
		t.Errorf("a cut notice: %v", err)
	}
}
