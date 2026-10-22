// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strconv"

	"latere.ai/x/sdk-go/latere"
)

// EventType names the kind of an event of an experiment's stream.
type EventType string

// The events of an experiment's stream, each with the type its Data
// decodes into. A stream holds others too; an event of a type not listed
// here is still delivered, for its Data to be read as it is.
const (
	// EventSnapshot opens a stream that does not resume: the experiment as
	// it is, an *Experiment.
	EventSnapshot EventType = "snapshot"
	// EventExperiment is a change of the experiment's state or progress,
	// an ExperimentChange.
	EventExperiment EventType = "experiment"
	// EventCell is a change of a cell's state, a CellChange.
	EventCell EventType = "cell"
	// EventCellParked is a cell that parked, a CellChange whose
	// ParkedReason says why. It follows the EventCell of the same change.
	EventCellParked EventType = "cell.parked"
	// EventTrial is a change of a trial's state, a TrialChange.
	EventTrial EventType = "trial"
	// EventGraded is a grader that filled a trial's values, a Grading.
	EventGraded EventType = "graded"
	// EventComparison is a comparison computed again, a ComparisonChange.
	EventComparison EventType = "comparison"
	// EventExperimentCompleted is the experiment completed, an
	// ExperimentEnd.
	EventExperimentCompleted EventType = "experiment.completed"
	// EventExperimentCanceled is the experiment canceled, an
	// ExperimentEnd.
	EventExperimentCanceled EventType = "experiment.canceled"
	// EventEnd is the stream's last event, once the experiment finished
	// and every event before it was delivered: a StreamEnd with the state
	// it finished in. The loop ends after it.
	EventEnd EventType = frameEnd
)

// StreamEnd is the Data of an EventEnd.
type StreamEnd struct {
	// State is the state the experiment finished in, ExperimentComplete
	// or ExperimentCanceled.
	State ExperimentState `json:"state"`
}

// A StreamEvent is one event of an experiment's stream.
type StreamEvent struct {
	// Seq is the event's place in the experiment's log, from 1, which
	// StreamEventsParams.AfterSeq resumes after. An EventSnapshot's is the
	// place of the last event it reflects, zero before any.
	Seq int64
	// Type is the event's kind.
	Type EventType
	// Data is the event's content as the API sent it, whose shape depends
	// on Type; read it with Decode.
	Data json.RawMessage
}

// Decode unmarshals the event's Data into v, such as a *CellChange for an
// event of type EventCell.
func (e *StreamEvent) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("evals: decoding the %s event %d: %w", e.Type, e.Seq, err)
	}
	return nil
}

// ExperimentChange is the Data of an EventExperiment.
type ExperimentChange struct {
	// ID is the experiment's id.
	ID string `json:"id"`
	// State is its state from this event on.
	State ExperimentState `json:"state"`
	// Progress is its progress at this event.
	Progress Progress `json:"progress"`
}

// CellChange is the Data of an EventCell and an EventCellParked.
type CellChange struct {
	// ID is the cell's id.
	ID string `json:"id"`
	// Task and Candidate are the cell's task and candidate, by digest.
	Task      string `json:"task"`
	Candidate string `json:"candidate"`
	// State is its state from this event on.
	State CellState `json:"state"`
	// ParkedReason says why a parked cell is parked.
	ParkedReason string `json:"parked_reason,omitempty"`
}

// TrialChange is the Data of an EventTrial.
type TrialChange struct {
	// ID is the trial's id.
	ID string `json:"id"`
	// Cell is the id of the trial's cell.
	Cell string `json:"cell"`
	// Index is the trial's place among its cell's trials, from 0.
	Index int `json:"index"`
	// State is its state from this event on.
	State TrialState `json:"state"`
	// Outcome is how it ended, once it is TrialDone.
	Outcome TrialOutcome `json:"outcome,omitempty"`
	// RetryOf is the id of the trial this one runs again, when it is a
	// retry.
	RetryOf string `json:"retry_of,omitempty"`
	// Imported reports a trial a harness ran elsewhere and imported.
	Imported bool `json:"imported,omitempty"`
}

// Grading is the Data of an EventGraded.
type Grading struct {
	// Trial is the id of the trial graded.
	Trial string `json:"trial"`
	// Grader is the grader, by digest.
	Grader string `json:"grader"`
	// Generation counts the gradings of the trial by this grader, from 1;
	// a later one replaces the values of the one before.
	Generation int `json:"generation"`
	// Metrics counts the values it filled.
	Metrics int `json:"metrics"`
}

// ComparisonChange is the Data of an EventComparison.
type ComparisonChange struct {
	// ID is the comparison's id; ListExperimentComparisons lists the
	// comparison with its figures.
	ID string `json:"id"`
	// Candidate is the candidate compared, by digest.
	Candidate string `json:"candidate"`
	// ReferenceCandidate and Baseline are the reference side: a candidate,
	// by digest, or a baseline record, by id.
	ReferenceCandidate string `json:"reference_candidate,omitempty"`
	Baseline           string `json:"baseline,omitempty"`
	// Automatic reports the comparison the platform keeps of a candidate
	// against its experiment's reference.
	Automatic bool `json:"automatic"`
	// Final reports that its figures no longer change.
	Final bool `json:"final"`
	// Declared is, for a comparison its experiment declared, its place in
	// the experiment's Comparisons; nil otherwise.
	Declared *int `json:"declared,omitempty"`
}

// ExperimentEnd is the Data of an EventExperimentCompleted and an
// EventExperimentCanceled.
type ExperimentEnd struct {
	// ID is the experiment's id.
	ID string `json:"id"`
	// State is ExperimentComplete or ExperimentCanceled.
	State ExperimentState `json:"state"`
	// Budget names the budget the experiment was metered against.
	Budget string `json:"budget,omitempty"`
	// CreatedBy is who created the experiment.
	CreatedBy string `json:"created_by,omitempty"`
}

// A StreamError is the refusal an experiment's stream ends with once it
// is open, such as the caller losing the right to read the experiment.
// Before the stream opens, a refusal is an *latere.Error as for any call.
type StreamError struct {
	// Code is the API's stable error code, such as
	// "experiment_not_found".
	Code string
	// Message is one sentence about the error, written for a person.
	Message string
	// Details holds developer detail, such as "retryable".
	Details map[string]any
}

// Error returns the code and the message on one line.
func (e *StreamError) Error() string {
	return fmt.Sprintf("evals: the stream ended with %s: %s", e.Code, e.Message)
}

// Retryable reports whether the API said the same stream may succeed if
// it is opened again later.
func (e *StreamError) Retryable() bool {
	again, _ := e.Details["retryable"].(bool)
	return again
}

// Is reports whether target is latere.ErrNotFound for a stream that ended
// because the experiment can no longer be read.
func (e *StreamError) Is(target error) bool {
	return target == latere.ErrNotFound && e.Code == "experiment_not_found"
}

// StreamEventsParams say where an experiment's stream starts.
type StreamEventsParams struct {
	// AfterSeq resumes after the event of this Seq, delivering every event
	// after it and no EventSnapshot; zero starts with an EventSnapshot.
	AfterSeq int64
}

// The frames a stream ends with, which are no events of the log: the end,
// delivered as EventEnd, and an error, as a *StreamError.
const (
	frameEnd   = "end"
	frameError = "error"
)

// StreamEvents returns an iterator over the events of the experiment id as
// they happen: an EventSnapshot first unless params resumes, then every
// event in order. Once the experiment is complete or canceled and every
// event was delivered, an EventEnd with the state it finished in is the
// last event, and the loop ends with no error; for an experiment finished
// already, it follows the snapshot. When the connection drops, or the
// API closes it as the token it was opened with expires, it connects
// again and resumes after the last event it yielded, so no event is lost
// or repeated; a stream opened before the experiment's log held any event
// may deliver its EventSnapshot again. Stop the loop, or end ctx, to stop
// following; either releases the connection at once.
//
// An id the caller's context holds no experiment by yields an error
// matching latere.ErrNotFound before any event. A refusal once the stream
// is open is a *StreamError, the last item.
func (c *Client) StreamEvents(ctx context.Context, id string, params *StreamEventsParams) iter.Seq2[*StreamEvent, error] {
	return func(yield func(*StreamEvent, error) bool) {
		p, err := experimentPath(id, "/stream")
		if err != nil {
			yield(nil, err)
			return
		}
		req := &latere.Request{Method: http.MethodGet, Path: p}
		if params != nil && params.AfterSeq > 0 {
			req.Header = http.Header{"Last-Event-ID": {strconv.FormatInt(params.AfterSeq, 10)}}
		}
		for frame, err := range c.cfg.Stream(ctx, req, endsStream) {
			if err != nil {
				yield(nil, err)
				return
			}
			switch frame.Event {
			case frameEnd:
				ev, err := decodeFrame(frame)
				yield(ev, err)
				return
			case frameError:
				yield(nil, streamError(frame.Data))
				return
			}
			ev, err := decodeFrame(frame)
			if !yield(ev, err) || err != nil {
				return
			}
		}
	}
}

// endsStream reports the frames after which the API closes an
// experiment's stream.
func endsStream(f latere.Frame) bool { return f.Event == frameEnd || f.Event == frameError }

// decodeFrame reads one event of an experiment's stream.
func decodeFrame(f latere.Frame) (*StreamEvent, error) {
	ev := &StreamEvent{Type: EventType(f.Event), Data: f.Data}
	if f.ID != "" {
		seq, err := strconv.ParseInt(f.ID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("evals: the %s event's id %q is not a sequence: %w", f.Event, f.ID, err)
		}
		ev.Seq = seq
	}
	if !json.Valid(f.Data) {
		return nil, fmt.Errorf("evals: the %s event %d is not JSON", f.Event, ev.Seq)
	}
	return ev, nil
}

// streamError reads the error frame a stream ends with, in the API's
// error envelope.
func streamError(data []byte) error {
	var envelope struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Error.Code == "" {
		return fmt.Errorf("evals: the stream ended with an error frame that is not the API's error: %q", data)
	}
	e := envelope.Error
	return &StreamError{Code: e.Code, Message: e.Message, Details: e.Details}
}
