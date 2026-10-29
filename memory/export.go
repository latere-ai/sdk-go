// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"latere.ai/x/sdk-go/latere"
)

// ErrExportIncomplete is what an Export's Read returns in place of io.EOF
// when the zip ended before its end record: the API stopped the export
// partway, after it had begun to answer, and what was read is not the
// whole of it. Ask for the export again.
var ErrExportIncomplete = errors.New("memory: the export stopped before the end of its zip")

// The parts of a zip's end record, which closes every zip: a signature,
// fields of a fixed length, and a comment of up to 65535 bytes whose
// length the record's last field holds.
const (
	endSignature = "PK\x05\x06"
	endLength    = 22
	maxTail      = endLength + 0xFFFF
)

// An Export is the zip of a person's memory as ExportMemory opens it,
// read as it arrives, so it is never held whole. Copy it where it goes,
// then Close it; an export left open holds a connection.
type Export struct {
	body io.ReadCloser
	name string
	// tail is the last bytes read, as many as can hold the zip's end
	// record, which tells a whole zip from one cut short.
	tail []byte
}

// Read reads the next bytes of the zip. At the end of a zip that has its
// end record it returns io.EOF, and at the end of one cut short an error
// matching ErrExportIncomplete.
func (e *Export) Read(p []byte) (int, error) {
	n, err := e.body.Read(p)
	e.track(p[:n])
	if err == io.EOF && !zipEnds(e.tail) {
		return n, ErrExportIncomplete
	}
	return n, err
}

// Close ends the read and releases its connection.
func (e *Export) Close() error { return e.body.Close() }

// FileName returns the name the API gives the zip, such as
// "latere-memory-2026-10-29.zip", as a file name with no folder; empty
// when the answer named none.
func (e *Export) FileName() string { return e.name }

// track adds b to the tail, dropping from its start what is more than
// maxTail.
func (e *Export) track(b []byte) {
	e.tail = append(e.tail, b...)
	if over := len(e.tail) - maxTail; over > 0 {
		e.tail = append(e.tail[:0], e.tail[over:]...)
	}
}

// zipEnds reports whether tail ends with a zip's end record: its
// signature, followed by the record's fixed fields and exactly the
// comment its last field says.
func zipEnds(tail []byte) bool {
	for i := len(tail) - endLength; i >= 0; i-- {
		if string(tail[i:i+4]) != endSignature {
			continue
		}
		comment := int(binary.LittleEndian.Uint16(tail[i+endLength-2 : i+endLength]))
		if i+endLength+comment == len(tail) {
			return true
		}
	}
	return false
}

// exportName returns the file name an answer's Content-Disposition gives,
// without any folder, or "" when it gives none.
func exportName(h http.Header) string {
	_, params, err := mime.ParseMediaType(h.Get("Content-Disposition"))
	if err != nil {
		// An answer without the header, or with one that does not parse,
		// names no file; the zip itself is unaffected.
		return ""
	}
	name := path.Base(strings.ReplaceAll(params["filename"], `\`, "/"))
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}

// ExportMemory opens a zip of every memory of the person as Markdown
// files, as Latere keeps them: latere-memory/you/ for You and
// latere-memory/projects/<project name>-<project id>/ for each project,
// each folder with its MEMORY.md list and one file per memory. The caller
// copies it where it goes and closes it.
//
// The API answers the zip as it reads the memories, so an export can stop
// after it has begun: Read then returns an error matching
// ErrExportIncomplete in place of io.EOF, and the program asks again. An
// export is retried while it connects, as every read is; once its bytes
// arrive, reading them is the caller's, for as long as ctx allows.
func (c *Client) ExportMemory(ctx context.Context) (*Export, error) {
	req := &latere.Request{Method: http.MethodGet, Path: basePath + "/export", Header: http.Header{"Accept": {"application/zip"}}}
	resp, err := c.cfg.Open(ctx, req)
	if err != nil {
		return nil, withSentinel(err)
	}
	return &Export{body: resp.Body, name: exportName(resp.Header)}, nil
}
