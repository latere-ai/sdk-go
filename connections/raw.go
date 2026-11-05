// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package connections

import (
	"encoding/json/jsontext"
)

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) jsontext.Value {
	return append(jsontext.Value(nil), b...)
}
