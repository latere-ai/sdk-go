// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json/v2"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

// A recorded call reads with the layer that decided it, its rule, its
// score, its mode, its review probability and the decision service's
// suggestion.
func TestAToolUseReadsHowItWasDecided(t *testing.T) {
	raw := `{"tool_use_id":"toolu_1","name":"bash","input":{"command":"git push"},"verdict":"ask","reason":"on the organization's always-confirm list",
		"mode":"progressive","risk":{"score":0.6,"source":"rules/1","features":["vcs_push"]},"review_probability":1,
		"suggestion":{"source":"learned/3","verdict":"ask","approve":0.58,"uncertainty":0.71,"thresholds":{"allow_above":0.9,"block_below":0.25},"audit_rate":0.05,"reason":"between"},
		"decided_by":"always_confirm","decided_by_rule":"bash(git push*)"}`
	var u agents.ToolUse
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.DecidedBy != agents.LayerAlwaysConfirm || u.DecidedByRule != "bash(git push*)" || u.DecidedBy.Learned() {
		t.Errorf("decided by %q %q", u.DecidedBy, u.DecidedByRule)
	}
	if u.Mode != "progressive" || u.Risk == nil || u.Risk.Score != 0.6 || u.Risk.Source != "rules/1" || len(u.Risk.Features) != 1 {
		t.Errorf("mode %q, risk %+v", u.Mode, u.Risk)
	}
	if u.ReviewProbability == nil || *u.ReviewProbability != 1 {
		t.Errorf("review probability %v", u.ReviewProbability)
	}
	s := u.Suggestion
	if s == nil || s.Source != "learned/3" || s.Verdict != agents.VerdictAsk || s.Approve != 0.58 || s.Thresholds.AllowAbove != 0.9 || s.Thresholds.BlockBelow != 0.25 || s.AuditRate != 0.05 {
		t.Errorf("suggestion %+v", s)
	}
	if !agents.LayerLearned.Learned() || agents.LayerScore.Learned() {
		t.Error("only the learned layer is learned")
	}
	// A call recorded before the fields existed reads with none of them.
	var old agents.ToolUse
	if err := json.Unmarshal([]byte(`{"tool_use_id":"toolu_0","name":"bash","input":{},"verdict":"allow"}`), &old); err != nil || old.DecidedBy != "" || old.Risk != nil || old.Suggestion != nil || old.ReviewProbability != nil {
		t.Errorf("an older call: %+v, %v", old, err)
	}
}

// A session reads its approval policy, and a person's answer to a call
// reads who answered, the decision and the pattern it remembered.
func TestAPolicyAndAConfirmationAreRead(t *testing.T) {
	var s agents.Session
	raw := `{"id":"ses_1","agent":{"id":"agt_1","name":"helper","version":2},"initiator":{"subject":"usr_1","kind":"person"},"status":"idle","turn":1,"last_seq":9,
		"policy":{"mode":"progressive","always_confirm":["bash(git push*)"],"thresholds":{"flag_at":0.3,"ask_at":0.6,"block_at":0.9}},
		"created_at":"2026-11-01T09:00:00Z","updated_at":"2026-11-01T09:05:00Z"}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if p := s.Policy; p == nil || p.Mode != agents.PolicyProgressive || len(p.AlwaysConfirm) != 1 || p.AlwaysAllow != nil || p.Thresholds.AskAt != 0.6 || p.Thresholds.BlockAt != 0.9 {
		t.Errorf("policy %+v", s.Policy)
	}
	var local agents.Session
	if err := json.Unmarshal([]byte(`{"id":"ses_2","status":"idle"}`), &local); err != nil || local.Policy != nil {
		t.Errorf("a session without a policy: %+v, %v", local.Policy, err)
	}

	var c agents.ToolConfirmation
	if err := json.Unmarshal([]byte(`{"sender":{"subject":"usr_1","kind":"person"},"tool_use_id":"toolu_1","decision":"deny","note":"not on main","remember":"bash(git push*)"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Sender.Subject != "usr_1" || c.ToolUseID != "toolu_1" || c.ApprovalID != "" || c.Decision != agents.DecisionDeny || c.Note != "not on main" || c.Remember != "bash(git push*)" {
		t.Errorf("confirmation %+v", c)
	}
}

// A call of an organization's agent reads the organization its decision
// was sent under, and the session reads the organization that owns its
// agent; a suggestion reads how much the person's answers settle it. A
// call and a session recorded before these fields read with none.
func TestTheOrganizationOfACallIsRead(t *testing.T) {
	var s agents.Session
	if err := json.Unmarshal([]byte(`{"id":"ses_1","agent":{"id":"agt_1","name":"helper","version":1},"initiator":{"subject":"https://auth.example|usr_1","kind":"person"},"org":"org_1","status":"idle"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Org != "org_1" {
		t.Errorf("the session's org is %q", s.Org)
	}
	raw := `{"tool_use_id":"toolu_1","name":"bash","input":{},"verdict":"ask","decided_by":"score","decision_org":"org_1",
		"suggestion":{"source":"learned/3","verdict":"ask","approve":0.5,"uncertainty":0.9,"thresholds":{"allow_above":0.9,"block_below":0.25},"audit_rate":0.05,"evidence_ratio":0.2,"ask_kind":"unsure"}}`
	var u agents.ToolUse
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.DecisionOrg != "org_1" {
		t.Errorf("the call's decision org is %q", u.DecisionOrg)
	}
	if sg := u.Suggestion; sg == nil || sg.AskKind != agents.AskUnsure || sg.EvidenceRatio == nil || *sg.EvidenceRatio != 0.2 {
		t.Errorf("suggestion %+v", u.Suggestion)
	}

	var old agents.ToolUse
	if err := json.Unmarshal([]byte(`{"tool_use_id":"toolu_0","name":"bash","input":{},"verdict":"ask","suggestion":{"source":"learned/1","verdict":"ask","approve":0.5,"uncertainty":0.9,"thresholds":{"allow_above":0.9,"block_below":0.25},"audit_rate":0.05}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.DecisionOrg != "" || old.Suggestion.AskKind != "" || old.Suggestion.EvidenceRatio != nil {
		t.Errorf("an older call: %+v %+v", old, old.Suggestion)
	}
	var older agents.Session
	if err := json.Unmarshal([]byte(`{"id":"ses_0","status":"idle"}`), &older); err != nil || older.Org != "" {
		t.Errorf("an older session: %q, %v", older.Org, err)
	}
}
