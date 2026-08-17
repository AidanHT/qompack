package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrLineTooLong is returned by LineReader.ReadLine when a line exceeds the reader's maxLine
// (00-ARCHITECTURE.md §2.4's "1 MiB max line", or a tighter limit a caller configured). The reader
// has already discarded the oversize line by the time this returns, so the next ReadLine call
// starts at the next real frame boundary.
var ErrLineTooLong = errors.New("qompack: ipc line exceeds max")

// newline is the NDJSON frame terminator (00-ARCHITECTURE.md §2.4: "one request per line ...
// \n-terminated").
const newline = '\n'

// encodeBufPool pools the *bytes.Buffer EncodeRequest/EncodeResponse write into, so the hot path —
// one encode per tool call — allocates a fresh buffer only on pool-miss rather than on every call.
var encodeBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// encodeLine marshals v as compact JSON with HTML escaping disabled (a captured prompt or tool
// result may legitimately contain "<", "&", ">", none of which are HTML in this context) through a
// pooled buffer, and returns a copy the caller owns outright — the pooled buffer is reset and
// returned to the pool before encodeLine returns, so nothing survives past that point except the
// copy. json.Encoder.Encode already appends exactly one '\n'; nothing here adds a second.
func encodeLine(v any) ([]byte, error) {
	buf, _ := encodeBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer encodeBufPool.Put(buf)

	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("ipc: encode: %w", err)
	}
	return bytes.Clone(buf.Bytes()), nil
}

// EncodeRequest renders req as one NDJSON line: compact JSON, HTML escaping disabled, exactly one
// trailing '\n' (00-ARCHITECTURE.md §2.4).
func EncodeRequest(req Request) ([]byte, error) {
	b, err := encodeLine(req)
	if err != nil {
		return nil, fmt.Errorf("ipc: EncodeRequest: %w", err)
	}
	return b, nil
}

// DecodeRequest parses line (with or without its trailing '\n' — encoding/json tolerates trailing
// whitespace after the value) as a Request. It never panics: arbitrary or malformed bytes produce
// an error, which is what FuzzDecodeRequest asserts for a corrupted spool line or a hostile peer.
func DecodeRequest(line []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Request{}, fmt.Errorf("ipc: DecodeRequest: %w", err)
	}
	return req, nil
}

// EncodeResponse renders resp the same way EncodeRequest renders a Request.
func EncodeResponse(resp Response) ([]byte, error) {
	b, err := encodeLine(resp)
	if err != nil {
		return nil, fmt.Errorf("ipc: EncodeResponse: %w", err)
	}
	return b, nil
}

// DecodeResponse is DecodeRequest's counterpart for Response.
func DecodeResponse(line []byte) (Response, error) {
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("ipc: DecodeResponse: %w", err)
	}
	return resp, nil
}

// LineReader reads NDJSON frames one line at a time off a stream, enforcing maxLine. It wraps
// bufio.Reader's own ReadSlice rather than ReadBytes/bufio.Scanner specifically so an oversize line
// is caught while it is still being accumulated — bounding memory rather than allocating the whole
// hostile line first — and so the stream can be resynchronized afterwards by discarding through the
// next '\n', leaving the following ReadLine call at a real frame boundary.
type LineReader struct {
	r       *bufio.Reader
	maxLine int
}

// NewLineReader returns a LineReader over r. maxLine <= 0 defaults to MaxLineBytes, §2.4's protocol
// ceiling — a caller that forgets to set it gets the normal limit, not a reader that rejects every
// line.
func NewLineReader(r io.Reader, maxLine int) *LineReader {
	if maxLine <= 0 {
		maxLine = MaxLineBytes
	}
	return &LineReader{r: bufio.NewReader(r), maxLine: maxLine}
}

// ReadLine returns the next line, without its trailing '\n'. It returns ErrLineTooLong for a line
// exceeding maxLine — having already discarded it — or the underlying reader's own error (typically
// io.EOF) at a clean stream end or on a real I/O failure.
func (lr *LineReader) ReadLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := lr.r.ReadSlice(newline)
		if len(chunk) > 0 {
			line = append(line, chunk...)
		}
		if err == nil {
			break // chunk ended in '\n': the frame is complete.
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(line) > lr.maxLine {
				lr.discardToNewline()
				return nil, ErrLineTooLong
			}
			continue // still under budget: keep accumulating across bufio's internal fills.
		}
		// A real reader error (io.EOF or worse): whatever was accumulated is not a complete frame.
		return nil, err
	}
	if len(line) > lr.maxLine {
		// The line fit inside one or more ReadSlice calls but still exceeds maxLine once the
		// trailing '\n' arrived; already fully consumed, so no resync is needed.
		return nil, ErrLineTooLong
	}
	return bytes.TrimSuffix(line, []byte{newline}), nil
}

// discardToNewline consumes and drops bytes until the next '\n' (or the stream ends), so the
// oversize line ReadLine just rejected does not leave its tail to be misread as the start of the
// following frame.
func (lr *LineReader) discardToNewline() {
	for {
		_, err := lr.r.ReadSlice(newline)
		if err == nil || !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}
