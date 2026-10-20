// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import "encoding/json"

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
