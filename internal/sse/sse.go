// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package sse reads a server-sent event stream, the text/event-stream
// format of the HTML standard: frames of "field: value" lines ended by a
// blank line, with comment lines that start with a colon.
package sse

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// A Frame is one event of the stream.
type Frame struct {
	// ID is the frame's id field; empty when the frame had none.
	ID string
	// Event is the frame's event field; empty when the frame had none,
	// which the standard reads as "message".
	Event string
	// Data is the frame's data lines joined by newlines.
	Data []byte
}

// MaxLine bounds one line of the stream; a longer line ends the read with
// bufio.ErrTooLong. An event's JSON is one line, and the API takes request
// bodies of at most 8 MiB, so twice that leaves room for what the server
// adds to an event. The buffer grows to it only for a line that needs it.
const MaxLine = 16 << 20

// A Reader reads frames from a stream.
type Reader struct {
	s *bufio.Scanner
	// heard reports that the stream sent at least one line.
	heard bool
}

// NewReader returns a Reader over r.
func NewReader(r io.Reader) *Reader {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), MaxLine)
	return &Reader{s: s}
}

// Heard reports whether the stream has sent anything yet: a frame, a
// comment such as a keepalive, or a blank line. A server that sends a
// keepalive is serving the stream even when it has no event to send.
func (r *Reader) Heard() bool { return r.heard }

// Next returns the next frame that carries data or an id. It returns
// io.EOF when the stream ends cleanly between frames, io.ErrUnexpectedEOF
// when it ends inside one, and the read's error otherwise. Comments and
// fields other than id, event and data are skipped.
func (r *Reader) Next() (Frame, error) {
	var (
		f       Frame
		data    bytes.Buffer
		started bool
		hasData bool
	)
	for r.s.Scan() {
		r.heard = true
		line := r.s.Text()
		if line == "" {
			if hasData || f.ID != "" {
				f.Data = data.Bytes()
				return f, nil
			}
			// A blank line after only comments or unknown fields ends
			// nothing a reader wants.
			f, started = Frame{}, false
			continue
		}
		started = true
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			f.ID = value
		case "event":
			f.Event = value
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		}
	}
	if err := r.s.Err(); err != nil {
		return Frame{}, err
	}
	if started && (hasData || f.ID != "") {
		return Frame{}, io.ErrUnexpectedEOF
	}
	return Frame{}, io.EOF
}
