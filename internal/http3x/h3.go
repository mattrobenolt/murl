package http3x

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/quic-go/qpack"
	"github.com/quic-go/quic-go/quicvarint"
	"go.withmatt.com/murl/internal/httpx"
	"golang.org/x/net/quic"
)

const (
	StreamControl = 0x00

	FrameData     = 0x00
	FrameHeaders  = 0x01
	FrameSettings = 0x04
)

type readBuffer [32 * 1024]byte

type Transport struct {
	conn    *quic.Conn
	stream  *quic.Stream
	verbose bool
}

func New(conn *quic.Conn, verbose bool) *Transport {
	return &Transport{
		conn:    conn,
		verbose: verbose,
	}
}

func (t *Transport) Start(ctx context.Context) error {
	// opens our control stream
	send, err := t.conn.NewSendOnlyStream(ctx)
	if err != nil {
		return err
	}
	send.SetWriteContext(ctx)
	startupFrame := [3]byte{
		StreamControl,
		FrameSettings,
		0,
	}
	send.Write(startupFrame[:])
	if err := send.Flush(); err != nil {
		return err
	}
	recv, err := t.conn.AcceptStream(ctx)
	if err != nil {
		return err
	}
	recv.SetReadContext(ctx)
	if !recv.IsReadOnly() {
		return errors.New("expected control stream")
	}
	streamType, err := recv.ReadByte()
	if err != nil {
		return err
	}
	if streamType != StreamControl {
		return errors.New("expected control stream")
	}

	header, err := readFrameHeader(recv)
	if err != nil {
		return err
	}
	fmt.Println(header)
	if header.Type != FrameSettings {
		return errors.New("expected settings frame")
	}
	size := header.Length
	for size > 0 {
		var setting Setting
		if err := setting.Read(recv); err != nil {
			return err
		}
		size -= uint64(setting.Len())
		fmt.Println(setting)
	}
	return nil
}

func (t *Transport) WriteRequest(ctx context.Context, req *httpx.Request) error {
	var hbuf bytes.Buffer
	henc := qpack.NewEncoder(&hbuf)
	henc.WriteField(qpack.HeaderField{Name: ":authority", Value: req.Authority})
	henc.WriteField(qpack.HeaderField{Name: ":method", Value: req.Method.String()})
	henc.WriteField(qpack.HeaderField{Name: ":path", Value: req.Path})
	henc.WriteField(qpack.HeaderField{Name: ":scheme", Value: req.Scheme.String()})

	for _, h := range req.Headers {
		henc.WriteField(qpack.HeaderField{Name: strings.ToLower(h.Name), Value: h.Value})
	}

	var err error
	t.stream, err = t.conn.NewStream(ctx)
	if err != nil {
		return err
	}

	t.stream.SetWriteContext(ctx)

	if err := writeHeaders(t.stream, hbuf.Bytes()); err != nil {
		return err
	}
	if req.Body == nil {
		t.stream.CloseWrite()
		return nil
	}

	go func() {
		defer t.stream.CloseWrite()
		if closer, ok := req.Body.(io.Closer); ok {
			defer closer.Close()
		}
		var readBuf readBuffer
		for {
			rn, rerr := req.Body.Read(readBuf[:])
			if rn > 0 {
				if werr := writeData(t.stream, readBuf[:rn]); werr != nil {
					t.conn.Abort(werr)
					return
				}
			}
			if rerr != nil {
				if rerr == io.EOF {
					return
				}
				t.conn.Abort(rerr)
				return
			}
		}
	}()
	return nil
}

func (t *Transport) ReadResponse(ctx context.Context, fn func(qpack.HeaderField)) error {
	t.stream.SetReadContext(ctx)
	for {
		frameHeader, err := readFrameHeader(t.stream)
		if err != nil {
			return err
		}
		fmt.Println(frameHeader)
		if frameHeader.Type != FrameHeaders {
			if frameHeader.Length > 0 {
				io.CopyN(io.Discard, t.stream, int64(frameHeader.Length))
			}
			continue
		}
		dec := qpack.NewDecoder(fn)
		_, err = io.CopyN(dec, t.stream, int64(frameHeader.Length))
		return err
	}
}

func (t *Transport) ReadBody(ctx context.Context, w io.Writer) error {
	defer t.stream.Close()
	var readBuf readBuffer
	var reader io.LimitedReader
	reader.R = t.stream
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frameHeader, err := readFrameHeader(t.stream)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if frameHeader.Type != FrameData {
			if frameHeader.Length > 0 {
				io.CopyN(io.Discard, t.stream, int64(frameHeader.Length))
			}
			continue
		}
		fmt.Println(frameHeader)
		reader.N = int64(frameHeader.Length)
		if _, err := io.CopyBuffer(w, &reader, readBuf[:]); err != nil {
			return err
		}
	}
}

func readFrameHeader(stream *quic.Stream) (FrameHeader, error) {
	var header FrameHeader
	var err error
	header.Type, err = quicvarint.Read(stream)
	if err != nil {
		return header, err
	}
	header.Length, err = quicvarint.Read(stream)
	return header, err
}

type FrameHeader struct {
	Type   uint64
	Length uint64
}

func (h FrameHeader) String() string {
	return fmt.Sprintf("[FrameHeader Type=0x%x Length=%d]", h.Type, h.Length)
}

type Setting struct {
	ID, Val uint64
}

func (s *Setting) Read(r *quic.Stream) error {
	var err error
	s.ID, err = quicvarint.Read(r)
	if err != nil {
		return err
	}
	s.Val, err = quicvarint.Read(r)
	if err != nil {
		return err
	}
	return nil
}

func writeHeaders(stream *quic.Stream, data []byte) error {
	stream.WriteByte(FrameHeaders)
	stream.Write(quicvarint.Append(nil, uint64(len(data))))
	stream.Write(data)
	return stream.Flush()
}

func writeData(stream *quic.Stream, data []byte) error {
	stream.WriteByte(FrameData)
	stream.Write(quicvarint.Append(nil, uint64(len(data))))
	stream.Write(data)
	return stream.Flush()
}

func (s Setting) String() string {
	return fmt.Sprintf("[SETTING Type=0x%x Val=%d]", s.ID, s.Val)
}

func (s Setting) Len() int {
	return quicvarint.Len(s.ID) + quicvarint.Len(s.Val)
}
