// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
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
