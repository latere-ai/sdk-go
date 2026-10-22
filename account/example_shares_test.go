// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

// Sharing a conversation by link, up to its last finished turn.
func ExampleClient_CreateShare() {
	s, err := client().CreateShare(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP")
	var apiErr *latere.Error
	switch {
	case errors.As(err, &apiErr) && apiErr.Code == "nothing_to_share":
		fmt.Println("there is nothing to share until the first answer is finished")
	case err != nil:
		log.Fatal(err)
	default:
		fmt.Println(s.URL)
	}
}

// Listing the links of one conversation, and saying which show less than
// it holds now.
func ExampleClient_ListShares() {
	for s, err := range client().ListShares(context.Background(), &account.ListSharesParams{SessionID: "ses_01M34G2JF01B91B8399CD1G6YP"}) {
		if err != nil {
			log.Fatal(err)
		}
		switch {
		case s.State == account.ShareSuspended:
			fmt.Println(s.URL, "does not work now:", s.Reason)
		case s.SessionLastSeq > s.UpToSeq:
			fmt.Println(s.URL, "shows less than the conversation holds now")
		default:
			fmt.Println(s.URL)
		}
	}
}

// Moving a link to the conversation's last finished turn, so its readers
// see what followed.
func ExampleClient_UpdateShare() {
	s, err := client().UpdateShare(context.Background(), "shr_01JA7Q1C2D3E4F5G6H7J8K9M0N")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.URL, "now shows up to", s.UpToSeq)
}

// Stopping a link for everyone at once. Stopping it again succeeds too.
func ExampleClient_DeleteShare() {
	if err := client().DeleteShare(context.Background(), "shr_01JA7Q1C2D3E4F5G6H7J8K9M0N"); err != nil {
		log.Fatal(err)
	}
}
