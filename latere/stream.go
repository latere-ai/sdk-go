// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"

	"latere.ai/x/sdk-go/internal/sse"
)

// A Frame is one event of a server-sent event stream.
type Frame struct {
	// ID is the frame's id; the stream resumes after the last one read.
	// Empty for a frame the server does not replay, such as a live delta.
	ID string
	// Event names the frame's kind.
	Event string
	// Data is the frame's payload, usually JSON.
	Data []byte
}

// Stream sends r, a request for a server-sent event stream, and returns an
// iterator over its frames. When the connection drops before done reports a
// frame as the stream's last, Stream connects again with the Last-Event-ID
// header naming the last frame read, so no frame is lost or repeated. It
// waits between attempts with the configured backoff, and gives up with an
// error after the configured number of retries in a row that read no frame.
//
// The iteration ends after the frame done reports, when the loop stops,
// when ctx ends, or with an error. Each yielded frame's Data is the caller's
// to keep. done may be nil for a stream that ends only with ctx.
func (c *Config) Stream(ctx context.Context, r *Request, done func(Frame) bool) iter.Seq2[Frame, error] {
	return func(yield func(Frame, error) bool) {
		req := *r
		req.Header = r.Header.Clone()
		if req.Header == nil {
			req.Header = http.Header{}
		}
		req.Header.Set("Accept", "text/event-stream")
		failures := 0
		for {
			resp, err := c.Open(ctx, &req)
			if err != nil {
				yield(Frame{}, err)
				return
			}
			lastID, read, finished, stopped, readErr := c.readFrames(resp, done, yield)
			closeBody(resp)
			if finished || stopped {
				return
			}
			if ctx.Err() != nil {
				yield(Frame{}, fmt.Errorf("latere: the stream of %s: %w", r.Path, ctx.Err()))
				return
			}
			if lastID != "" {
				req.Header.Set("Last-Event-ID", lastID)
			}
			if read > 0 {
				failures = 0
			}
			if failures >= c.maxRetries {
				yield(Frame{}, fmt.Errorf("latere: the stream of %s dropped: %w", r.Path, readErr))
				return
			}
			if err := c.sleep(ctx, c.backoff(failures)); err != nil {
				yield(Frame{}, fmt.Errorf("latere: the stream of %s: %w", r.Path, err))
				return
			}
			failures++
		}
	}
}

// readFrames yields the frames of one connection. It returns the id of the
// last frame with one, how many frames it read, whether done reported the
// last frame, whether the loop stopped, and the error that ended the
// connection otherwise.
func (c *Config) readFrames(resp *http.Response, done func(Frame) bool, yield func(Frame, error) bool) (lastID string, read int, finished, stopped bool, err error) {
	r := sse.NewReader(resp.Body)
	for {
		f, err := r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return lastID, read, false, false, err
		}
		read++
		frame := Frame{ID: f.ID, Event: f.Event, Data: f.Data}
		if frame.ID != "" {
			lastID = frame.ID
		}
		if !yield(frame, nil) {
			return lastID, read, false, true, nil
		}
		if done != nil && done(frame) {
			return lastID, read, true, false, nil
		}
	}
}
