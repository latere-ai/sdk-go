// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"strings"
	"sync"

	"latere.ai/x/sdk-go/internal/sse"
	"latere.ai/x/sdk-go/latere"
)

// Stream is an answer arriving as events. Range over Events once; the
// connection closes when the loop ends, whether the answer finished, the
// loop stopped early, or an error ended it. Close it as well, with defer,
// in case Events is never ranged over.
type Stream struct {
	events    iter.Seq2[Event, error]
	loss      []string
	requestID string
	closeOnce sync.Once
	close     func() error
	closeErr  error
}

// NewStream returns a Stream over events, for a program that produces Lux
// dialect events another way, such as a test double, or a client that
// translates a provider's own stream. loss and requestID are what Loss and
// RequestID report. close, which may be nil, releases what events reads
// from; Stream calls it once, when the range over Events ends or when Close
// is called.
func NewStream(events iter.Seq2[Event, error], loss []string, requestID string, close func() error) *Stream {
	if close == nil {
		close = func() error { return nil }
	}
	return &Stream{events: events, loss: loss, requestID: requestID, close: close}
}

// Events returns the answer's events, in order. The iteration ends after
// EventMessageStop, when the loop stops, or with an error: a *StreamError
// when the service reported a failure partway, io.ErrUnexpectedEOF when the
// answer was cut off before EventMessageStop, or the context's error when
// it ended. A Stream's events can be ranged over once.
func (s *Stream) Events() iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		defer func() { _ = s.Close() }()
		for ev, err := range s.events {
			if !yield(ev, err) || err != nil {
				return
			}
		}
	}
}

// Loss names the request's fields the model's provider could not
// represent, which were left out rather than dropped silently.
func (s *Stream) Loss() []string { return s.loss }

// RequestID names the request in the service's records; quote it when you
// report a problem.
func (s *Stream) RequestID() string { return s.requestID }

// Close releases the connection. It is safe to call more than once, and
// after the range over Events has closed the stream.
func (s *Stream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.close() })
	return s.closeErr
}

// StreamError is a failure the service reported after the answer had begun
// to stream, such as the model's provider failing partway.
type StreamError struct {
	// Code is the service's error code, such as "upstream_error"; programs
	// branch on it.
	Code string
	// Message is one sentence about the failure, written for a person.
	Message string
	// Detail is the developer's account of this failure, when the service
	// gave one.
	Detail string
	// RequestID names the request in the service's records.
	RequestID string
}

// Error returns the code, message, detail and request id on one line.
func (e *StreamError) Error() string {
	var b strings.Builder
	b.WriteString("llm: the stream failed")
	if e.Code != "" {
		fmt.Fprintf(&b, " (%s)", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, " (%s)", e.Detail)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request %s)", e.RequestID)
	}
	return b.String()
}

// Stream sends req and returns its answer as a Stream of events, once the
// service has accepted the request. It sets req's Stream to true on the copy
// it sends. A refusal returns a *latere.Error before any event.
//
// The Config's timeout bounds the wait for the service to accept the
// request; the events then arrive for as long as ctx allows.
//
// Stream is sent once, never again after a failure, since the model may
// have run and been billed: a dropped connection ends the iteration with
// io.ErrUnexpectedEOF.
func (c *Client) Stream(ctx context.Context, req *Request) (*Stream, error) {
	if req == nil {
		return nil, errNoRequest
	}
	wire := *req
	wire.Stream = true
	r := c.request(generatePath, &wire, latere.RetryNever)
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.Header.Set("Accept", "text/event-stream")
	resp, err := c.cfg.Open(ctx, r)
	if err != nil {
		return nil, err
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/event-stream" {
		closeBody(resp)
		return nil, fmt.Errorf("llm: the answer is %q, not an event stream", resp.Header.Get("Content-Type"))
	}
	requestID := resp.Header.Get(requestIDHeader)
	events := func(yield func(Event, error) bool) {
		readEvents(ctx, resp.Body, requestID, yield)
	}
	return NewStream(events, parseLoss(resp.Header), requestID, resp.Body.Close), nil
}

// readEvents yields the events of one stream until message_stop, the
// caller's stop, or an error. Frames of a kind it does not know are
// skipped, so a service that adds one breaks no client.
func readEvents(ctx context.Context, body io.Reader, requestID string, yield func(Event, error) bool) {
	r := sse.NewReader(body)
	for {
		f, err := r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			yield(Event{}, fmt.Errorf("llm: reading the stream: %w", causeOr(ctx, err)))
			return
		}
		if f.Event == "error" {
			yield(Event{}, streamError(f.Data, requestID))
			return
		}
		if !knownEvent(EventType(f.Event)) {
			continue
		}
		var ev Event
		if err := json.Unmarshal(f.Data, &ev); err != nil {
			yield(Event{}, fmt.Errorf("llm: decoding a %s event: %w", f.Event, err))
			return
		}
		if ev.Type == "" {
			// The frame's event name is its type, when the data leaves it
			// out.
			ev.Type = EventType(f.Event)
		}
		if !yield(ev, nil) || ev.Type == EventMessageStop {
			return
		}
	}
}

// knownEvent reports whether t is one of the stream's event kinds.
func knownEvent(t EventType) bool {
	switch t {
	case EventMessageStart, EventBlockStart, EventTextDelta, EventArgsDelta,
		EventThinkingDelta, EventSignatureDelta, EventBlockStop,
		EventMessageDelta, EventMessageStop:
		return true
	}
	return false
}

// streamError reads an error frame: the service's error envelope, whose
// code an older service writes as type, or, for data that is not one, the
// data as the message.
func streamError(data []byte, requestID string) *StreamError {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
			Details struct {
				Detail    string `json:"detail"`
				RequestID string `json:"request_id"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil || (envelope.Error.Code == "" && envelope.Error.Type == "" && envelope.Error.Message == "") {
		return &StreamError{Message: strings.TrimSpace(string(data)), RequestID: requestID}
	}
	code := envelope.Error.Code
	if code == "" {
		code = envelope.Error.Type
	}
	e := &StreamError{
		Code:      code,
		Message:   envelope.Error.Message,
		Detail:    envelope.Error.Details.Detail,
		RequestID: envelope.Error.Details.RequestID,
	}
	if e.RequestID == "" {
		e.RequestID = requestID
	}
	return e
}
