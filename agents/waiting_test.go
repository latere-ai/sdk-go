// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// logOf builds a log of events, each a type and its payload as JSON, at
// sequences from 1, the event at seq n with id "evt_<n>". An entry whose
// type ends in "@sub" belongs to the subagent "sub".
func logOf(t *testing.T, entries ...string) []agents.Event {
	t.Helper()
	var out []agents.Event
	for i := 0; i < len(entries); i += 2 {
		seq := int64(i/2 + 1)
		e := agents.Event{ID: fmt.Sprintf("evt_%d", seq), Seq: seq, SessionID: "ses_1", Payload: json.RawMessage(entries[i+1])}
		typ, sub := strings.CutSuffix(entries[i], "@sub")
		if sub {
			e.Subagent = "sub"
		}
		e.Type = agents.EventType(typ)
		if !json.Valid(e.Payload) {
			t.Fatalf("entry %d is not JSON: %s", i/2, e.Payload)
		}
		out = append(out, e)
	}
	return out
}

const (
	running = `{"status":"running"}`
	ended   = `{"status":"idle","stop_reason":"end_turn"}`
	cutNow  = `{"status":"idle","stop_reason":"interrupted","detail":"send_now"}`
)

func says(text string) string {
	return fmt.Sprintf(`{"sender":{"subject":"alice","kind":"person"},"content":[{"type":"text","text":%q}]}`, text)
}

// states maps each waiting message's text to its state.
func states(ws []agents.WaitingMessage) map[string]agents.MessageState {
	out := map[string]agents.MessageState{}
	for _, w := range ws {
		out[w.Message.Content[0].Text] = w.State
	}
	return out
}

func TestWaitingMessages(t *testing.T) {
	for name, tc := range map[string]struct {
		log  []string
		want map[string]agents.MessageState
	}{
		"a message sent while idle starts its turn and does not wait": {
			log:  []string{"user.message", says("Start."), "session.status", running, "session.status", ended},
			want: map[string]agents.MessageState{},
		},
		"a message the agent has not read waits": {
			log:  []string{"user.message", says("Start."), "session.status", running, "user.message", says("Use 9000.")},
			want: map[string]agents.MessageState{"Use 9000.": agents.MessageWaiting},
		},
		"a message a read names is read": {
			log: []string{"session.status", running, "user.message", says("Use 9000."), "messages.read", `{"event_ids":["evt_2"]}`,
				"model.request", `{"model":"m","fold_seq":3,"names_reads":true}`, "session.status", ended},
			want: map[string]agents.MessageState{"Use 9000.": agents.MessageRead},
		},
		"a withdrawn message stays withdrawn whatever names it after": {
			log: []string{"session.status", running, "user.message", says("Skip them."), "user.withdraw", `{"sender":{"subject":"alice","kind":"person"},"event_id":"evt_2"}`,
				"messages.read", `{"event_ids":["evt_2"]}`},
			want: map[string]agents.MessageState{"Skip them.": agents.MessageWithdrawn},
		},
		"a withdrawal after the read changes nothing": {
			log: []string{"session.status", running, "user.message", says("Lint it."), "messages.read", `{"event_ids":["evt_2"]}`,
				"user.withdraw", `{"sender":{"subject":"alice","kind":"person"},"event_id":"evt_2"}`},
			want: map[string]agents.MessageState{"Lint it.": agents.MessageRead},
		},
		"a request recorded before reads were named read what its fold covers": {
			log: []string{"session.status", running, "user.message", says("First."), "model.request", `{"model":"m","fold_seq":2}`,
				"user.message", says("Second.")},
			want: map[string]agents.MessageState{"First.": agents.MessageRead, "Second.": agents.MessageWaiting},
		},
		"a title's request reads nothing": {
			log:  []string{"session.status", running, "user.message", says("First."), "model.request", `{"model":"m","fold_seq":2,"purpose":"title"}`},
			want: map[string]agents.MessageState{"First.": agents.MessageWaiting},
		},
		"a subagent's message and request are not the conversation's": {
			log: []string{"session.status", running, "user.message@sub", says("Inner."), "user.message", says("Outer."),
				"model.request@sub", `{"model":"m","fold_seq":3}`},
			want: map[string]agents.MessageState{"Outer.": agents.MessageWaiting},
		},
		"a running status ends a turn nobody ended, and a message after it waits in the new turn": {
			log:  []string{"session.status", running, "user.message", says("Before."), "session.status", running, "user.message", says("After.")},
			want: map[string]agents.MessageState{"Before.": agents.MessageWaiting, "After.": agents.MessageWaiting},
		},
		"a redacted message is left out": {
			log:  []string{"session.status", running, "user.message", `{"tombstone":true}`},
			want: map[string]agents.MessageState{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := states(agents.WaitingMessages(logOf(t, tc.log...)))
			if len(got) != len(tc.want) {
				t.Fatalf("states %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("states %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A message read by a marker records where it was read, and one sent now
// whose turn stopped for it says so; the order of the log is the order of
// the sequences, whatever order the events are passed in.
func TestAWaitingMessageSaysWhereItWasRead(t *testing.T) {
	evs := logOf(t,
		"session.status", running,
		"user.message", says("Lint it."),
		"user.message", `{"sender":{"subject":"alice","kind":"person"},"content":[{"type":"text","text":"Stop."}],"send_now":true}`,
		"session.status", cutNow,
		"session.status", running,
		"messages.read", `{"event_ids":["evt_2","evt_3"]}`,
	)
	evs[0], evs[5] = evs[5], evs[0]
	ws := agents.WaitingMessages(evs)
	if len(ws) != 2 {
		t.Fatalf("%d waiting messages, want 2", len(ws))
	}
	if ws[0].Event.ID != "evt_2" || ws[0].ReadSeq != 6 || ws[0].SentNowStop {
		t.Errorf("the first %+v, want evt_2 read at 6", ws[0])
	}
	if ws[1].Event.ID != "evt_3" || ws[1].ReadSeq != 6 || !ws[1].SentNowStop || !ws[1].Message.SendNow {
		t.Errorf("the second %+v, want evt_3 sent now, its turn stopped for it", ws[1])
	}
}

func TestSendingNowAndWithdrawing(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"POST /v1/agents/sessions/ses_1/events": event})
	if _, err := s.SendMessage(t.Context(), "ses_1", &agents.SendMessageParams{Content: []agents.ContentBlock{agents.Text("Stop.")}, SendNow: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Withdraw(t.Context(), "ses_1", "evt_8"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"payload":{"content":[{"text":"Stop.","type":"text"}],"send_now":true},"type":"user.message"}`,
		`{"payload":{"event_id":"evt_8"},"type":"user.withdraw"}`,
	}
	for i, w := range want {
		got, err := json.Marshal((*calls)[i].Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != w {
			t.Errorf("event %d sent %s, want %s", i, got, w)
		}
	}
	if _, err := s.Withdraw(t.Context(), "ses_1", ""); err == nil {
		t.Error("a withdrawal of no event: no error")
	}
	if len(*calls) != 2 {
		t.Errorf("%d events sent, want the 2 that hold", len(*calls))
	}
}

func TestAWithdrawalTheAgentReadFirstIsAConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"conflict","message":"The message was already read.","details":{"detail":"read"}}}`)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.New(cfg).Withdraw(t.Context(), "ses_1", "evt_8"); !errors.Is(err, latere.ErrConflict) {
		t.Fatalf("withdraw: %v, want a conflict", err)
	}
}
