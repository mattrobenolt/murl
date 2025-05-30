package http1

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.withmatt.com/murl/internal/httpx"
)

type readBuffer [32 * 1024]byte

var CRLF = []byte("\r\n")

type Transport struct {
	w             io.Writer
	r             io.Reader
	br            *bufio.Reader
	contentLength int64
	isHead        bool
	verbose       bool
}

func New(w io.Writer, r io.Reader, verbose bool) *Transport {
	return &Transport{
		w:             w,
		r:             r,
		br:            bufio.NewReader(r),
		contentLength: -1,
		verbose:       verbose,
	}
}

func writeHeader(b *bytes.Buffer, h httpx.Header) {
	b.WriteString(h.Name)
	b.WriteString(": ")
	b.WriteString(h.Value)
	b.Write(CRLF)
}

func (t *Transport) WriteRequest(ctx context.Context, req *httpx.Request, fn func(httpx.Header)) error {
	contentLength := req.ContentLength
	if req.Body != nil {
		if contentLength == -1 {
			if len, ok := req.Body.(interface{ Len() int }); ok {
				contentLength = int64(len.Len())
			}
		}
		if closer, ok := req.Body.(io.Closer); ok {
			defer closer.Close()
		}
	}

	const protoHTTP11 = "HTTP/1.1"

	fn(httpx.Header{Name: ":method", Value: req.Method.String()})
	fn(httpx.Header{Name: ":path", Value: req.Path})
	fn(httpx.Header{Name: ":proto", Value: protoHTTP11})

	hasTransferEncoding := false

	var buf bytes.Buffer
	buf.WriteString(req.Method.String())
	buf.WriteByte(' ')
	buf.WriteString(req.Path)
	buf.WriteByte(' ')
	buf.WriteString(protoHTTP11)
	buf.Write(CRLF)
	if req.Authority != "" {
		h := httpx.Header{Name: "Host", Value: req.Authority}
		writeHeader(&buf, h)
		fn(h)
	}
	for _, h := range req.Headers {
		writeHeader(&buf, h)
		fn(h)
		switch {
		case contentLength == -1 && httpx.HeaderEqual(h.Name, "content-length"):
			l, ok := httpx.Atoi64(h.Value)
			if !ok {
				continue
			}
			contentLength = l
		case httpx.HeaderEqual(h.Name, "transfer-encoding"):
			if strings.Contains(h.Value, "chunked") {
				if t.contentLength > -1 {
					return errors.New("got Content-Length and Transfer-Encoding: chunked")
				}
				hasTransferEncoding = true
			}
		}
	}

	t.isHead = req.Method == httpx.MethodHead
	hasBody := req.Body != nil && !t.isHead && contentLength != 0

	if contentLength == -1 && hasBody && !hasTransferEncoding {
		h := httpx.Header{Name: "Transfer-Encoding", Value: "chunked"}
		writeHeader(&buf, h)
		fn(h)
	}

	buf.Write(CRLF)
	if _, err := buf.WriteTo(t.w); err != nil {
		return err
	}
	if !hasBody {
		return nil
	}

	switch contentLength {
	case -1:
		return t.writeChunkedBody(ctx, req.Body)
	default:
		return t.writeBodyNormal(ctx, req.Body, contentLength)
	}
}

func (t *Transport) ReadResponse(ctx context.Context, fn func(httpx.Header)) error {
	line, err := readLine(t.br)
	if err != nil {
		return err
	}
	proto, status, ok := cutByte(line, ' ')
	if !ok {
		return errors.New("malformed response")
	}
	fn(httpx.Header{Name: ":proto", Value: string(proto)})
	fn(httpx.Header{Name: ":status", Value: string(bytes.TrimSpace(status))})

	chunkedEncoding := false

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := readLine(t.br)
		if err != nil {
			return err
		}
		if bytes.Equal(line, CRLF) {
			break
		}
		name, value, ok := cutByte(line, ':')
		if !ok {
			return errors.New("malformed headers")
		}

		headerName := string(name)
		headerValue := string(bytes.TrimSpace(value))
		fn(httpx.Header{Name: headerName, Value: headerValue})

		switch {
		case httpx.HeaderEqual(headerName, "content-length"):
			if chunkedEncoding {
				return errors.New("got Content-Length and Transfer-Encoding: chunked")
			}
			len, err := strconv.ParseUint(headerValue, 10, 63)
			if err != nil {
				return err
			}
			t.contentLength = int64(len)
		case httpx.HeaderEqual(headerName, "transfer-encoding"):
			if strings.Contains(headerValue, "chunked") {
				if t.contentLength > -1 {
					return errors.New("got Content-Length and Transfer-Encoding: chunked")
				}
				chunkedEncoding = true
			}
		}
	}

	// If transfer-encoding is chunked, override content-length
	if chunkedEncoding {
		t.contentLength = -1
	}

	return ctx.Err()
}

func (t *Transport) ReadBody(ctx context.Context, w io.Writer) error {
	if t.isHead || t.contentLength == 0 {
		return ctx.Err()
	}
	switch t.contentLength {
	case -1:
		return t.readChunkedBody(ctx, w)
	default:
		return t.readNormalBody(ctx, w)
	}
}

func (t *Transport) writeBodyNormal(ctx context.Context, r io.Reader, contentLength int64) error {
	var readBuf readBuffer
	return copyBuffer(ctx, t.w, io.LimitReader(r, contentLength), readBuf[:])
}

func (t *Transport) writeChunkedBody(ctx context.Context, r io.Reader) error {
	var readBuf readBuffer
	bw := bufio.NewWriterSize(t.w, len(readBuf)+6)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, err := r.Read(readBuf[:])
		if n > 0 {
			bw.Write(appendHex(n, bw.AvailableBuffer()))
			bw.Write(CRLF)
			bw.Write(readBuf[:n])
			bw.Write(CRLF)
			if err := bw.Flush(); err != nil {
				return err
			}
		}

		if err != nil {
			if err == io.EOF {
				bw.WriteByte('0')
				bw.Write(CRLF)
				bw.Write(CRLF)
				return bw.Flush()
			}
			return err
		}
	}
}

func (t *Transport) readNormalBody(ctx context.Context, w io.Writer) error {
	var readBuf readBuffer
	return copyBuffer(ctx, w, io.LimitReader(t.br, t.contentLength), readBuf[:])
}

func (t *Transport) readChunkedBody(ctx context.Context, w io.Writer) error {
	var readBuf readBuffer
	var limitReader io.LimitedReader
	limitReader.R = t.br

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// read line, and parse as hex to get the size of the next read
		line, err := readLine(t.br)
		if err != nil {
			return err
		}
		line, ok := bytes.CutSuffix(line, CRLF)
		if len(line) == 0 {
			continue
		}
		if !ok {
			return errors.New("malformed chunk encoding")
		}
		size, err := hexToInt(line)
		if err != nil {
			return err
		}

		if size > 0 {
			limitReader.N = size

			if err := copyBuffer(ctx, w, &limitReader, readBuf[:]); err != nil {
				return err
			}
		}

		// read remaining CRLF
		_, err = t.br.Read(readBuf[:2])
		if err != nil || !bytes.Equal(readBuf[:2], CRLF) {
			return err
		}

		// last chunk is size 0
		if size == 0 {
			return nil
		}
	}
}

func (t *Transport) ReadTrailers(ctx context.Context, fn func(httpx.Header)) error {
	if t.isHead || t.contentLength != -1 {
		return ctx.Err()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := readLine(t.br)
		if err != nil {
			return err
		}
		if bytes.Equal(line, CRLF) {
			return ctx.Err()
		}
		name, value, ok := cutByte(line, ':')
		if !ok {
			return errors.New("malformed headers")
		}

		headerName := string(name)
		headerValue := string(bytes.TrimSpace(value))
		fn(httpx.Header{Name: headerName, Value: headerValue})
	}
}

func cutByte(s []byte, sep byte) (before, after []byte, found bool) {
	if i := bytes.IndexByte(s, sep); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, nil, false
}

func appendHex(n int, buf []byte) []byte {
	if n == 0 {
		return append(buf, '0')
	}

	// Find how many hex digits we need
	temp := n
	digits := 0
	for temp > 0 {
		temp >>= 4
		digits++
	}

	// Grow the buffer to accommodate the hex digits
	start := len(buf)
	for range digits {
		buf = append(buf, 0)
	}

	// Write hex digits from right to left
	for i := digits - 1; i >= 0; i-- {
		digit := n & 0xF
		if digit < 10 {
			buf[start+i] = byte('0' + digit)
		} else {
			buf[start+i] = byte('a' + digit - 10)
		}
		n >>= 4
	}

	return buf
}

func hexToInt(b []byte) (int64, error) {
	// Handle empty input
	if len(b) == 0 {
		return 0, errors.New("empty hex string")
	}

	// Prevent too large inputs
	if len(b) > 16 {
		return 0, errors.New("hex string too long (max 16 characters)")
	}

	var n int64
	for _, c := range b {
		// Check for overflow before multiplying
		if n > (1<<60 - 1) {
			return 0, errors.New("hex value too large")
		}
		n *= 16
		switch {
		case '0' <= c && c <= '9':
			n += int64(c - '0')
		case 'a' <= c && c <= 'f':
			n += int64(c - 'a' + 10)
		case 'A' <= c && c <= 'F':
			n += int64(c - 'A' + 10)
		default:
			return 0, fmt.Errorf("invalid hex character: %c", c)
		}
	}
	return n, nil
}

func copyBuffer(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			if ew != nil {
				return ew
			}
			if nr != nw {
				return io.ErrShortWrite
			}
		}
		if er != nil {
			if er != io.EOF {
				return er
			}
			return nil
		}
	}
}

func readLine(b *bufio.Reader) ([]byte, error) {
	full, frag, n, err := collectFragments(b, '\n')
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if full == nil {
		return frag, nil
	}
	buf := make([]byte, n)
	n = 0
	for i := range full {
		n += copy(buf[n:], full[i])
	}
	copy(buf[n:], frag)
	return buf, nil
}

func collectFragments(b *bufio.Reader, delim byte) (fullBuffers [][]byte, finalFragment []byte, totalLen int, err error) {
	var frag []byte
	// Use ReadSlice to look for delim, accumulating full buffers.
	for {
		var e error
		frag, e = b.ReadSlice(delim)
		if e == nil { // got final fragment
			break
		}
		if e != bufio.ErrBufferFull { // unexpected error
			err = e
			break
		}

		// Make a copy of the buffer.
		buf := bytes.Clone(frag)
		fullBuffers = append(fullBuffers, buf)
		totalLen += len(buf)
	}

	totalLen += len(frag)
	return fullBuffers, frag, totalLen, err
}
