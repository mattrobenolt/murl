package netx

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/quic"
)

func Lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
}

func DialTCP(addr netip.AddrPort) (*net.TCPConn, error) {
	return net.DialTCP("tcp", nil, net.TCPAddrFromAddrPort(addr))
}

func DialTLS(ctx context.Context, addr netip.AddrPort, cfg *tls.Config) (*tls.Conn, error) {
	conn, err := DialTCP(addr)
	if err != nil {
		return nil, err
	}
	tconn := tls.Client(conn, cfg)
	if err := tconn.HandshakeContext(ctx); err != nil {
		tconn.Close()
		return nil, err
	}
	return tconn, nil
}

// func DialQUIC(ctx context.Context, addr netip.AddrPort, cfg *tls.Config) (quic.EarlyConnection, error) {
// 	// conn, err := net.ListenUDP("udp", nil)
// 	// if err != nil {
// 	// 	return nil, err
// 	// }
// 	// tr := &quic.Transport{Conn: conn}
// 	// qconn, err := tr.DialEarly(ctx, net.UDPAddrFromAddrPort(addr), cfg, &quic.Config{
// 	// 	MaxIncomingStreams:    1,
// 	// 	MaxIncomingUniStreams: 1,
// 	// 	Versions:              []quic.Version{quic.Version1},
// 	// })
// 	// if err != nil {
// 	// 	return nil, err
// 	// }
// 	// select {
// 	// case <-qconn.HandshakeComplete():
// 	// 	return qconn, nil
// 	// case <-ctx.Done():
// 	// 	return nil, ctx.Err()
// 	// }
// }

func DialQUIC(ctx context.Context, addr netip.AddrPort, cfg *tls.Config) (*quic.Conn, error) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	ep, err := quic.NewEndpoint(conn, nil)
	if err != nil {
		return nil, err
	}
	return ep.Dial(ctx, "udp", addr.String(), &quic.Config{
		TLSConfig:        cfg,
		HandshakeTimeout: 5 * time.Second,
	})
}
