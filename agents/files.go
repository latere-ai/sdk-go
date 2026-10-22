// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"latere.ai/x/sdk-go/latere"
)

// MaxSessionFileBytes is the largest file ReadSessionFile reads from a
// session's working directory; a larger one is refused with an error
// matching ErrFileTooLarge, never cut short.
const MaxSessionFileBytes = 32 << 20

// Errors of ReadSessionFile to test with errors.Is. The error that
// matches one wraps the *latere.Error the API answered, which errors.As
// still finds, with its status, its message and its request id.
var (
	// ErrFileUnavailable matches a read of a session's working directory
	// while the session's machine is not running: it is stopped, was never
	// started, or is gone. A read never starts a machine; the file can be
	// read once the session runs its machine again, as its next turn does.
	// The files the session kept, which ReadBlob reads, stay readable. The
	// API answers it with 409, so latere.ErrConflict matches it too.
	ErrFileUnavailable = errors.New("agents: the session's machine is not running, so its files cannot be read now")
	// ErrFileTooLarge matches a read of a file past MaxSessionFileBytes.
	// The *latere.Error's Details hold the file's "size" and the "limit",
	// in bytes.
	ErrFileTooLarge = errors.New("agents: the file is too large to read from the session's working directory")
)

// codeError is an *latere.Error that also matches the sentinel of its
// API code, for a refusal a caller branches on whose status alone does
// not tell it apart, such as a 409 that is no conflict of state.
type codeError struct {
	err      *latere.Error
	sentinel error
}

func (e *codeError) Error() string { return e.err.Error() }

// Is reports whether target is the sentinel of the error's code; the
// *latere.Error it unwraps to matches the sentinels of its status.
func (e *codeError) Is(target error) bool { return target == e.sentinel }

// Unwrap returns the API's answer.
func (e *codeError) Unwrap() error { return e.err }

// withSentinel returns err joined with the sentinel codes names for the
// API's code, when err is an *latere.Error of one of them, and err
// otherwise.
func withSentinel(err error, codes map[string]error) error {
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	if sentinel, ok := codes[apiErr.Code]; ok {
		return &codeError{err: apiErr, sentinel: sentinel}
	}
	return err
}

// fileCodes are the API's codes for the refusals of a read of a
// session's working directory that have a sentinel.
var fileCodes = map[string]error{
	"file_unavailable": ErrFileUnavailable,
	"file_too_large":   ErrFileTooLarge,
}

// A FileReader is a session's file as ReadBlob or ReadSessionFile opens
// it: its bytes, its media type and its length. Read it, then Close it;
// a reader left open holds a connection.
type FileReader struct {
	body         io.ReadCloser
	mediaType    string
	size         int64
	cacheControl string
	etag         string
	disposition  string
}

// newFileReader reads a file's answer.
func newFileReader(resp *http.Response) *FileReader {
	h := resp.Header
	return &FileReader{body: resp.Body, mediaType: h.Get("Content-Type"), size: resp.ContentLength,
		cacheControl: h.Get("Cache-Control"), etag: h.Get("ETag"), disposition: h.Get("Content-Disposition")}
}

// Read reads the next bytes of the file. A file whose answer was cut off
// before the length it named ends with io.ErrUnexpectedEOF.
func (r *FileReader) Read(p []byte) (int, error) { return r.body.Read(p) }

// Close ends the read and releases its connection.
func (r *FileReader) Close() error { return r.body.Close() }

// MediaType returns the media type the API answered the bytes as, from
// the answer's Content-Type, such as "image/png".
func (r *FileReader) MediaType() string { return r.mediaType }

// Size returns the file's length in bytes, or -1 when the answer did not
// say.
func (r *FileReader) Size() int64 { return r.size }

// CacheControl returns the answer's Cache-Control, which says how long the
// bytes may be kept and by whom, such as "private, max-age=31536000,
// immutable" for a file a session kept by its digest; empty when the
// answer stated none. A program that passes the file on to a browser
// sends it with the file.
func (r *FileReader) CacheControl() string { return r.cacheControl }

// ETag returns the answer's entity tag, empty when it stated none.
func (r *FileReader) ETag() string { return r.etag }

// ContentDisposition returns the answer's Content-Disposition, such as
// "attachment" with the file's name made safe to save under for a file
// that is no picture; empty when the answer stated none. A program that
// passes the file on to a browser sends it with the file, so a file the
// platform answers as a download is never shown in the program's own
// page.
func (r *FileReader) ContentDisposition() string { return r.disposition }

// acceptAny asks for an answer of any media type: a file's bytes, which
// the API answers an error to as JSON all the same.
func acceptAny() http.Header { return http.Header{"Accept": {"*/*"}} }

// ReadBlob opens a file the session keeps, by its digest: a file a
// message carried, by its Attachment's Blob, or a picture an answer
// showed or a file it linked, by its KeptFile's Blob. The caller closes
// the reader.
//
// A PNG, JPEG, GIF or WebP picture is answered as its own media type, and
// any other file as "application/octet-stream", whatever it is: the
// record that names the digest holds the file's own type and its size,
// in Attachment.MediaType and Size or KeptFile.MediaType and Size. The
// reader's Size is the file's when the session keeps its files in a
// folder, as a record's Ref shows, and -1 otherwise.
//
// The files a session keeps stay readable after it ends. A digest the
// session does not hold, or a file whose records were all redacted,
// returns an error matching latere.ErrNotFound. The Config's timeout
// bounds the wait for the answer, and after it only ctx bounds the read.
func (c *Client) ReadBlob(ctx context.Context, sessionID, digest string) (*FileReader, error) {
	if sessionID == "" {
		return nil, errNoID
	}
	// A digest is an algorithm and a hash, such as "sha256:" and its hex,
	// which also keeps a path segment of "." or ".." out of the route.
	if algorithm, sum, ok := strings.Cut(digest, ":"); !ok || algorithm == "" || sum == "" {
		return nil, fmt.Errorf("agents: ReadBlob: %q is not a digest, such as an Attachment's Blob", digest)
	}
	req := &latere.Request{Method: http.MethodGet, Path: sessionPath(sessionID, "/blobs/"+url.PathEscape(digest)), Header: acceptAny()}
	resp, err := c.cfg.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	return newFileReader(resp), nil
}

// ReadSessionFile opens a file of the session's working directory as it
// is now, such as a file the agent wrote. path is inside the working
// directory: absolute, such as a KeptFile's Resolved, or relative to it,
// such as a SkippedFile's Path. Read a file an answer linked from its
// KeptFile with ReadBlob instead, which works after the session's machine
// stops. The caller closes the reader.
//
// The file is read from the session's machine while it runs: a machine
// that is not running returns an error matching ErrFileUnavailable, and a
// file past MaxSessionFileBytes one matching ErrFileTooLarge. A path
// outside the working directory, a directory, or a file that holds
// credentials is refused with 400, and a file that does not exist returns
// an error matching latere.ErrNotFound. The media type is the one the
// file's name or its leading bytes name, and the size is the file's when
// the read began; a file that shrank while it was read ends with
// io.ErrUnexpectedEOF. The Config's timeout bounds the wait for the
// answer, and after it only ctx bounds the read.
func (c *Client) ReadSessionFile(ctx context.Context, sessionID, path string) (*FileReader, error) {
	if sessionID == "" {
		return nil, errNoID
	}
	if path == "" {
		return nil, errors.New("agents: ReadSessionFile: the path is empty")
	}
	req := &latere.Request{Method: http.MethodGet, Path: sessionPath(sessionID, "/files"), Query: url.Values{"path": {path}}, Header: acceptAny()}
	resp, err := c.cfg.Open(ctx, req)
	if err != nil {
		return nil, withSentinel(err, fileCodes)
	}
	return newFileReader(resp), nil
}
