// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"fmt"
	"log"
)

// Showing how full a conversation's context is, and what fills it.
func ExampleContextReport_UsedFraction() {
	ctx := context.Background()
	s, err := client().GetSession(ctx, "ses_01M34G2JF01B91B8399CD1G6YP")
	if err != nil {
		log.Fatal(err)
	}
	r := s.ContextReport
	full, ok := r.UsedFraction()
	if !ok {
		return
	}
	fmt.Printf("%.0f%% of %d tokens\n", full*100, r.Window)
	for _, p := range r.PartsInOrder() {
		fmt.Printf("  %s: %d\n", p.Part, p.Tokens)
	}
}
