package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/signal"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"go.withmatt.com/murl/internal/http1"
	"go.withmatt.com/murl/internal/http2x"
	"go.withmatt.com/murl/internal/httpx"
	"go.withmatt.com/murl/internal/netx"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	done := make(chan error)
	go func() {
		done <- realMain(ctx)
	}()
	select {
	case <-ctx.Done():
		die(ctx.Err())
	case err := <-done:
		die(err)
	}
}

func realMain(ctx context.Context) error {
	var body io.Reader
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		body = os.Stdin
	}
	hostname, method, path := os.Args[1], os.Args[2], os.Args[3]
	req := httpx.Request{
		Authority: hostname,
		Method:    httpx.ParseMethod(method),
		Path:      path,
		Scheme:    httpx.SchemeHTTPS,
		Body:      body,
		Headers: []httpx.Header{
			{Name: "user-agent", Value: "mattware"},
		},
	}

	addrs, err := netx.Lookup(ctx, req.Authority)
	if err != nil {
		return err
	}
	addr := netip.AddrPortFrom(addrs[0], 443)
	conn, err := netx.DialTLS(ctx, addr, &tls.Config{
		ServerName: req.Authority,
		// NextProtos: []string{http2.NextProtoTLS, "http/1.1"},
		NextProtos: []string{"http/1.1"},
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.VerifyHostname(req.Authority); err != nil {
		return err
	}

	state := conn.ConnectionState()
	if state.NegotiatedProtocol == http2.NextProtoTLS {
		tr := http2x.New(conn, conn, true)
		if err := tr.Start(ctx); err != nil {
			return err
		}
		if err := tr.WriteRequest(ctx, &req); err != nil {
			return err
		}
		if err := tr.ReadResponse(ctx, func(f hpack.HeaderField) {
			if f.Sensitive {
				fmt.Printf("  %s = %q (SENSITIVE)\n", f.Name, f.Value)
			} else {
				fmt.Printf("  %s = %q\n", f.Name, f.Value)
			}
		}); err != nil {
			return err
		}

		return tr.ReadBody(ctx, os.Stdout)
	} else {
		tr := http1.New(conn, conn, true)
		if err := tr.WriteRequest(ctx, &req); err != nil {
			return err
		}
		if err := tr.ReadResponse(ctx, func(h httpx.Header) {
			fmt.Printf("  %s = %q\n", h.Name, h.Value)
		}); err != nil {
			return err
		}
		if err := tr.ReadBody(ctx, os.Stdout); err != nil {
			return err
		}
		return tr.ReadTrailers(ctx, func(h httpx.Header) {
			fmt.Printf("  %s = %q\n", h.Name, h.Value)
		})
	}
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}
