// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"iter"
)

// A Page is one page of a list the API answers in pages.
type Page[T any] struct {
	// Items are the page's items, in the list's order.
	Items []T `json:"items"`
	// NextCursor asks for the page after this one; empty on the last page.
	NextCursor string `json:"next_cursor,omitempty"`
}

// Items returns an iterator over every item of a paged list. It calls fetch
// for the first page with an empty cursor, then with each page's
// NextCursor while the loop wants more, so a loop that stops early fetches
// no further page. An error from fetch is yielded once with a zero item,
// and the iteration ends.
//
// The capability packages build their List methods on it; use it for a
// list endpoint they do not cover yet.
func Items[T any](ctx context.Context, fetch func(ctx context.Context, cursor string) (*Page[T], error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cursor := ""
		for {
			page, err := fetch(ctx, cursor)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
			if page.NextCursor == "" || page.NextCursor == cursor {
				return
			}
			cursor = page.NextCursor
		}
	}
}
