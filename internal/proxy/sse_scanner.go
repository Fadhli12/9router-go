package proxy

import (
	"bufio"
	"bytes"
	"io"
	"sync"
)

const (
	scannerInitBufSize = 64 * 1024
	scannerMaxLineSize = 10 * 1024 * 1024 // Allow lines up to 10MB (tool calls, multimodal data)
)

var scannerBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, scannerInitBufSize)
		return &b
	},
}

func acquireScannerBuf() *[]byte {
	return scannerBufPool.Get().(*[]byte)
}

func releaseScannerBuf(b *[]byte) {
	if b == nil || cap(*b) > 1024*1024 {
		return
	}
	*b = (*b)[:scannerInitBufSize]
	scannerBufPool.Put(b)
}

// ScanStream reads an SSE stream from r, accumulates complete events
// (per the SSE spec: fields separated by \n, an event ends at a blank
// line), joins all `data:` fields of each event into one payload, and
// invokes onChunk once per complete event.
//
// It correctly handles multi-line `data:` payloads, CRLF line endings, and
// `data:` keep-alives (empty payload → no callback). A single optional
// leading space after the colon is stripped per the spec.
func ScanStream(r io.Reader, onChunk func([]byte)) error {
	scanner := bufio.NewScanner(r)
	bufPtr := acquireScannerBuf()
	defer releaseScannerBuf(bufPtr)
	scanner.Buffer(*bufPtr, scannerMaxLineSize)

	var dataLines [][]byte
	emit := func() {
		if len(dataLines) == 0 {
			return
		}
		payload := bytes.Join(dataLines, []byte("\n"))
		dataLines = dataLines[:0]
		if len(payload) == 0 {
			return // keep-alive: `data:` with empty payload
		}
		onChunk(payload)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		// Blank line terminates the current event.
		if len(line) == 0 {
			emit()
			continue
		}
		// Comment lines (": keep-alive") are ignored.
		if line[0] == ':' {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			chunk := bytes.TrimPrefix(line, []byte("data:"))
			// Per SSE spec, strip a single optional leading space.
			if len(chunk) > 0 && chunk[0] == ' ' {
				chunk = chunk[1:]
			}
			dataLines = append(dataLines, chunk)
		}
	}
	emit() // flush trailing event with no terminating blank line
	return scanner.Err()
}
