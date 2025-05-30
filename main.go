package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.withmatt.com/murl/internal/http1"
	"go.withmatt.com/murl/internal/httpx"
	"go.withmatt.com/murl/internal/netx"
)

type connKey struct {
	hostname string
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *flagVerbose {
		stderr = &debugWriter{*bufio.NewWriter(os.Stderr)}
	} else {
		stderr = noopWriter{}
	}

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
	if *flagHelp {
		program.SetOutput(os.Stdout)
		program.Usage()
		os.Exit(0)
	}

	urls := program.Args()
	if len(urls) == 0 {
		fmt.Fprintf(os.Stderr, "%s: try %s --help\n", program.Name(), program.Name())
		os.Exit(2)
	}

	req := httpx.Request{
		Method: httpx.ParseMethod(*flagMethod),
		Headers: httpx.Headers{
			{Name: "Accept", Value: "*/*"},
			{Name: "User-Agent", Value: fmt.Sprintf("%s/1.0", program.Name())},
		},
		ContentLength: -1,
	}

	if *flagCompressed {
		req.Headers = append(req.Headers, httpx.Header{
			Name:  "Accept-Encoding",
			Value: "gzip, zstd, br",
		})
	}

	if *flagJSON != "" {
		*flagData = *flagJSON
		req.Headers = append(req.Headers, httpx.Header{
			Name:  "Content-Type",
			Value: "application/json",
		})
		req.Headers[0].Value = "application/json"
	}
	if *flagUser != "" {
		username, password, found := strings.Cut(*flagUser, ":")
		if !found {
			panic("invalid username:password")
		}
		req.Headers = append(req.Headers, httpx.Header{
			Name:  "Authorization",
			Value: "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
		})
	}
	for _, header := range *flagHeader {
		key, value, found := strings.Cut(header, ":")
		if !found {
			panic("invalid header" + header)
		}
		req.Headers = req.Headers.Set(key, strings.TrimSpace(value))
	}
	out, onlyPrintable := openOutput()
	_ = onlyPrintable
	defer out.Close()

	var stringReader *strings.Reader
	if *flagData != "" {
		stringReader = strings.NewReader(*flagData)
		req.Body = stringReader
		req.ContentLength = int64(len(*flagData))
		if *flagMethod == "" {
			req.Method = httpx.MethodPost
		}
	}

	for i := 0; i < len(urls); {
		url := urls[i]
		u := must(parseURL(url))

		hostname, hostport := splitHostPort(u.Host)
		switch u.Scheme {
		case "http":
			req.Scheme = httpx.SchemeHTTP
		case "https":
			req.Scheme = httpx.SchemeHTTPS
		}

		req.Authority = hostname
		req.Path = u.Path

		ips := must(lookup(ctx, hostname))
		port, ok := httpx.ParsePort(req.Scheme, hostport)
		if !ok {
			panic("invalid port")
		}

		stderr.WriteString("* Host ")
		stderr.WriteString(hostname)
		stderr.WriteByte(':')
		stderr.WriteUint64(uint64(port))
		stderr.WriteString(hostport)
		stderr.WriteString(" was resolved.\n")
		stderr.WriteString("* IPv4:")
		first := true
		for _, ip := range ips {
			if first {
				first = false
				stderr.WriteByte(' ')
			} else {
				stderr.WriteString(", ")
			}
			stderr.WriteAddr(ip)
		}
		stderr.WriteByte('\n')
		stderr.Flush()

		switch req.Scheme {
		case httpx.SchemeHTTP:
			conn := must(dialTCP(req.Authority, ips, port))

			tr := http1.New(conn, conn, *flagVerbose)
			die(tr.WriteRequest(ctx, &req, func(h httpx.Header) {
				switch h.Name {
				case ":method":
					stderr.WriteString("> ")
					stderr.WriteString(h.Value)
					stderr.WriteByte(' ')
				case ":path":
					stderr.WriteString(h.Value)
					stderr.WriteByte(' ')
				case ":proto":
					stderr.WriteString(h.Value)
					stderr.WriteByte('\n')
				default:
					stderr.WriteString("> ")
					stderr.WriteString(h.Name)
					stderr.WriteString(": ")
					stderr.WriteString(h.Value)
					stderr.WriteByte('\n')
				}
			}))
			stderr.WriteString(">\n* Request completely sent off\n")
			stderr.Flush()

			shouldClose := false
			statusCode := ""
			location := ""
			die(tr.ReadResponse(ctx, func(h httpx.Header) {
				switch h.Name {
				case ":proto":
					stderr.WriteString("< ")
					stderr.WriteString(h.Value)
					stderr.WriteByte(' ')
				case ":status":
					statusCode = h.Value[:3]
					stderr.WriteString(h.Value)
					stderr.WriteByte('\n')
				default:
					switch {
					case httpx.HeaderEqual(h.Name, "connection") && h.Value == "close":
						shouldClose = true
					case httpx.HeaderEqual(h.Name, "location"):
						location = h.Value
					}
					stderr.WriteString("< ")
					stderr.WriteString(h.Name)
					stderr.WriteString(": ")
					stderr.WriteString(h.Value)
					stderr.WriteByte('\n')
				}
			}))
			stderr.WriteString("<\n")
			stderr.Flush()

			if *flagLocation {
				switch statusCode {
				case "301", "302", "307", "308":
					tr.ReadBody(ctx, io.Discard)
					// tr.ReadTrailers(ctx, func(h httpx.Header) {})
					if shouldClose {
						stderr.WriteString("* Closing connection\n")
						conn.Close()
						delete(tcpCache, conn.RemoteAddr().(*net.TCPAddr).AddrPort())
					} else {
						stderr.WriteString("* Connection to host ")
						stderr.WriteString(hostname)
						stderr.WriteString(" left intact\n")
					}
					urls[i] = location
					stderr.WriteString("* Redirecting to ")
					stderr.WriteString(location)
					stderr.WriteString("\n\n")
					stderr.Flush()
					continue
				}
			}

			die(tr.ReadBody(ctx, out))
			// die(tr.ReadTrailers(ctx, func(h httpx.Header) {}))

			if shouldClose {
				stderr.WriteString("* Closing connection\n")
				conn.Close()
				delete(tcpCache, conn.RemoteAddr().(*net.TCPAddr).AddrPort())
			} else {
				stderr.WriteString("* Connection to host ")
				stderr.WriteString(hostname)
				stderr.WriteString(" left intact\n")
			}
			stderr.Flush()
		}

		if stringReader != nil {
			stringReader.Seek(0, io.SeekStart)
		}
		i++
	}
	return ctx.Err()
}

var tcpCache map[netip.AddrPort]*net.TCPConn

func dialTCP(hostname string, ips []netip.Addr, port uint16) (*net.TCPConn, error) {
	var lastErr error
	for _, ip := range ips {
		addrport := netip.AddrPortFrom(ip, port)
		if conn, ok := tcpCache[addrport]; ok {
			stderr.WriteString("* Re-using existing connection with host ")
			stderr.WriteString(hostname)
			stderr.WriteByte('\n')
			stderr.Flush()
			return conn, nil
		}
		stderr.WriteString("*   Trying ")
		stderr.WriteAddrPort(addrport)
		stderr.WriteString("...\n")
		stderr.Flush()
		conn, err := netx.DialTCP(addrport)
		if err == nil {
			stderr.WriteString("* Connected to ")
			stderr.WriteString(hostname)
			stderr.WriteString(" (")
			stderr.WriteAddr(ip)
			stderr.WriteString(") port ")
			stderr.WriteUint64(uint64(port))
			stderr.WriteByte('\n')
			stderr.Flush()
			if tcpCache == nil {
				tcpCache = map[netip.AddrPort]*net.TCPConn{}
			}
			tcpCache[addrport] = conn
			return conn, nil
		}
		lastErr = errors.Join(lastErr, err)
	}
	return nil, lastErr
}

var dnsCache map[string][]netip.Addr

func lookup(ctx context.Context, hostname string) ([]netip.Addr, error) {
	if ips, ok := dnsCache[hostname]; ok {
		return ips, nil
	}

	ips, err := netx.Lookup(ctx, hostname)
	if err != nil {
		return nil, err
	}
	if dnsCache == nil {
		dnsCache = make(map[string][]netip.Addr)
	}
	dnsCache[hostname] = ips
	return ips, nil
}
