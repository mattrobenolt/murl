package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/signal"
	"strconv"

	"github.com/quic-go/qpack"
	"github.com/quic-go/quic-go/http3"

	"go.withmatt.com/murl/internal/http3x"
	"go.withmatt.com/murl/internal/httpx"
	"go.withmatt.com/murl/internal/netx"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	done := make(chan error)
	go func() { done <- realMain(ctx) }()
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

	hostname, port, method, path := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	req := httpx.Request{
		Authority: hostname,
		Method:    httpx.ParseMethod(method),
		Path:      path,
		Scheme:    httpx.SchemeHTTPS,
		Headers: []httpx.Header{
			{Name: "user-agent", Value: "mattware"},
		},
		Body: body,
	}

	addrs, err := netx.Lookup(ctx, req.Authority)
	if err != nil {
		return err
	}
	addr := netip.AddrPortFrom(addrs[0], uint16(must(strconv.Atoi(port))))
	conn, err := netx.DialQUIC(ctx, addr, &tls.Config{
		ServerName: req.Authority,
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{http3.NextProtoH3},
	})
	if err != nil {
		return err
	}
	state := conn.ConnectionState()
	fmt.Println(state.NegotiatedProtocol)
	fmt.Println(req)

	tr := http3x.New(conn, true)
	if err := tr.Start(ctx); err != nil {
		return err
	}
	if err := tr.WriteRequest(ctx, &req); err != nil {
		return err
	}
	if err := tr.ReadResponse(ctx, func(h qpack.HeaderField) {
		fmt.Println(h)
	}); err != nil {
		return err
	}
	return tr.ReadBody(ctx, os.Stdout)
}

func must[T any](v T, err error) T {
	die(err)
	return v
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}
