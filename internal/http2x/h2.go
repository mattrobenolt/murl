package http2x

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"go.withmatt.com/murl/internal/httpx"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const (
	initialWindowSize     = 10 * 1024 * 1024 // 10MB
	windowUpdateThreshold = initialWindowSize * 3 / 4
	streamID              = 1
)

const h2TableSize = 4 << 10

type readBuffer [32 * 1024]byte

func New(w io.Writer, r io.Reader, verbose bool) *Transport {
	return &Transport{
		w:          w,
		r:          r,
		framer:     http2.NewFramer(w, r),
		windowSize: initialWindowSize,
		verbose:    verbose,
	}
}

type Transport struct {
	w          io.Writer
	r          io.Reader
	framer     *http2.Framer
	windowSize uint32
	verbose    bool
}

func (t *Transport) Start(ctx context.Context) error {
	t.framer.SetReuseFrames()
	if _, err := io.WriteString(t.w, http2.ClientPreface); err != nil {
		return err
	}
	if err := t.readSettings(ctx, func(s http2.Setting) error {
		fmt.Println("  ", s)
		return nil
	}); err != nil {
		return err
	}
	if err := t.framer.WriteSettings(
		http2.Setting{ID: http2.SettingInitialWindowSize, Val: initialWindowSize},
		http2.Setting{ID: http2.SettingMaxConcurrentStreams, Val: 1},
		http2.Setting{ID: http2.SettingMaxFrameSize, Val: 16384},
	); err != nil {
		return err
	}
	if err := t.framer.WriteWindowUpdate(0, initialWindowSize); err != nil {
		return err
	}
	return nil
}

func (t *Transport) WriteRequest(ctx context.Context, req *httpx.Request) error {
	var hbuf bytes.Buffer
	henc := hpack.NewEncoder(&hbuf)
	henc.WriteField(hpack.HeaderField{Name: ":authority", Value: req.Authority})
	henc.WriteField(hpack.HeaderField{Name: ":method", Value: req.Method.String()})
	henc.WriteField(hpack.HeaderField{Name: ":path", Value: req.Path})
	henc.WriteField(hpack.HeaderField{Name: ":scheme", Value: req.Scheme.String()})

	for _, h := range req.Headers {
		henc.WriteField(hpack.HeaderField{Name: strings.ToLower(h.Name), Value: h.Value})
	}

	hasBody := req.Body != nil
	if hasBody {
		if closer, ok := req.Body.(io.Closer); ok {
			defer closer.Close()
		}
	}
	if err := t.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		BlockFragment: hbuf.Bytes(),
		EndStream:     !hasBody,
		EndHeaders:    true,
	}); err != nil {
		return err
	}
	if !hasBody {
		return nil
	}
	var readBuf readBuffer
	for {
		rn, rerr := req.Body.Read(readBuf[:])
		if werr := t.framer.WriteData(streamID, rerr == io.EOF, readBuf[:rn]); werr != nil {
			return werr
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}

func (t *Transport) ReadResponse(ctx context.Context, fn func(hpack.HeaderField)) error {
	hdec := hpack.NewDecoder(h2TableSize, fn)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := t.framer.ReadFrame()
		if err != nil {
			return err
		}
		fmt.Println(frame)
		switch f := frame.(type) {
		case *http2.HeadersFrame:
			if _, err := hdec.Write(f.HeaderBlockFragment()); err != nil {
				return err
			}
			if f.HeadersEnded() {
				return nil
			}
		case *http2.WindowUpdateFrame:
			fmt.Printf("  Window Update: StreamID=%d, Increment=%d\n",
				f.StreamID, f.Increment)
		case *http2.GoAwayFrame:
			fmt.Printf("  GoAway: LastStreamID=%d, ErrCode=%v, Debug=%q\n",
				f.LastStreamID, f.ErrCode, f.DebugData())
			return io.ErrUnexpectedEOF
		case *http2.RSTStreamFrame:
			fmt.Printf("  RST Stream: StreamID=%d, ErrCode=%v\n",
				f.StreamID, f.ErrCode)
			return io.ErrUnexpectedEOF
		}
	}
}

func (t *Transport) ReadBody(ctx context.Context, w io.Writer) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := t.framer.ReadFrame()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(frame)

		switch f := frame.(type) {
		case *http2.DataFrame:
			dataLen := f.Length
			w.Write(f.Data())

			if t.windowSize -= dataLen; t.windowSize <= initialWindowSize-windowUpdateThreshold {
				increment := initialWindowSize - t.windowSize
				t.framer.WriteWindowUpdate(0, increment)
				t.framer.WriteWindowUpdate(f.StreamID, increment)
			}

			if f.StreamEnded() {
				return nil
			}
		case *http2.GoAwayFrame:
			fmt.Printf("  GoAway: LastStreamID=%d, ErrCode=%v, Debug=%q\n",
				f.LastStreamID, f.ErrCode, f.DebugData())
			return io.ErrUnexpectedEOF

		case *http2.RSTStreamFrame:
			fmt.Printf("  RST Stream: StreamID=%d, ErrCode=%v\n",
				f.StreamID, f.ErrCode)
			return io.ErrUnexpectedEOF

		case *http2.PingFrame:
			if f.IsAck() {
				fmt.Printf("  Received PING ACK\n")
			} else {
				if err := t.framer.WritePing(true, f.Data); err != nil {
					return err
				}
			}
		case *http2.WindowUpdateFrame:
			fmt.Printf("  Window Update: StreamID=%d, Increment=%d\n",
				f.StreamID, f.Increment)
		}
	}
}

func (t *Transport) readSettings(ctx context.Context, fn func(http2.Setting) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := t.framer.ReadFrame()
		if err != nil {
			return err
		}
		fmt.Println(frame)
		switch f := frame.(type) {
		case *http2.SettingsFrame:
			if err := f.ForeachSetting(fn); err != nil {
				return err
			}
			if err := t.framer.WriteSettingsAck(); err != nil {
				return err
			}
			return nil
		}
	}
}
