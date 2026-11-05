// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) jsontext.Value {
	return append(jsontext.Value(nil), b...)
}

// foreignJSON reads JSON the platform does not produce, such as the
// arguments a model wrote for a tool. Duplicate keys, invalid UTF-8 and
// any letter case in a member name are all accepted.
var foreignJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)
