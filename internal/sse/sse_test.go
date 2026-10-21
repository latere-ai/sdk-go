// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReaderReadsFrames(t *testing.T) {
	stream := ": keepalive\n\n" +
		"id: 1\nevent: user.message\ndata: {\"seq\":1}\n\n" +
		"retry: 1000\n\n" +
		"event: delta\ndata: one\ndata: two\n\n" +
		"id: 2\ndata:no-space\n\n"
	r := NewReader(strings.NewReader(stream))
	want := []Frame{
		{ID: "1", Event: "user.message", Data: []byte(`{"seq":1}`)},
		{Event: "delta", Data: []byte("one\ntwo")},
		{ID: "2", Data: []byte("no-space")},
	}
	for i, w := range want {
		got, err := r.Next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.ID != w.ID || got.Event != w.Event || string(got.Data) != string(w.Data) {
			t.Errorf("frame %d = %+v, want %+v", i, got, w)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("after the last frame: %v, want io.EOF", err)
	}
}

func TestReaderReportsATruncatedFrame(t *testing.T) {
	r := NewReader(strings.NewReader("id: 3\nevent: agent.message\ndata: {\"se"))
	if _, err := r.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a frame cut off: %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReaderReportsALineTooLong(t *testing.T) {
	r := NewReader(strings.NewReader("data: " + strings.Repeat("x", MaxLine+1) + "\n\n"))
	if _, err := r.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Errorf("a line past MaxLine: %v, want an error", err)
	}
}

func TestReaderEndsCleanlyAfterAComment(t *testing.T) {
	r := NewReader(strings.NewReader(": keepalive\n"))
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("a stream of a comment: %v, want io.EOF", err)
	}
}

func TestReaderHearsAKeepalive(t *testing.T) {
	silent := NewReader(strings.NewReader(""))
	if _, err := silent.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("an empty stream: %v, want io.EOF", err)
	}
	if silent.Heard() {
		t.Error("an empty stream was heard")
	}
	quiet := NewReader(strings.NewReader(": keepalive\n\n"))
	if quiet.Heard() {
		t.Error("heard before reading")
	}
	if _, err := quiet.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("a stream of a keepalive: %v, want io.EOF", err)
	}
	if !quiet.Heard() {
		t.Error("a keepalive was not heard")
	}
}
