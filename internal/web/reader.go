package web

import (
	"bytes"
	"io"
)

// http.ServeContent needs a ReadSeeker, and bytes.Reader is one. Wrapped in a
// function so each request gets its own cursor.
func newReader(b []byte) io.ReadSeeker { return bytes.NewReader(b) }
