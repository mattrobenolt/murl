package main

import (
	"errors"
	"io"
	"net/http"
	urlpkg "net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

func copyBuffer(dst io.Writer, src io.Reader, buf []byte, onlyPrintable bool) (written int64, err error) {
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			if onlyPrintable && !utf8.Valid(buf[0:nr]) {
				return written, errors.New("invalid UTF-8")
			}
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

func sortHeaders(h http.Header) []keyValues {
	s := make([]keyValues, 0, len(h))
	for k, vs := range h {
		s = append(s, keyValues{key: k, values: vs})
	}
	slices.SortFunc(s, func(a, b keyValues) int { return strings.Compare(a.key, b.key) })
	return s
}

type keyValues struct {
	key    string
	values []string
}

func parseURL(url string) (*urlpkg.URL, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	u, err := urlpkg.Parse(url)
	if err != nil {
		return nil, err
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

// splitHostPort separates host and port. If the port is not valid, it returns
// the entire input as host, and it doesn't check the validity of the host.
// Unlike net.SplitHostPort, but per RFC 3986, it requires ports to be numeric.
func splitHostPort(hostPort string) (host, port string) {
	host = hostPort

	colon := strings.LastIndexByte(host, ':')
	if colon != -1 && validOptionalPort(host[colon:]) {
		host, port = host[:colon], host[colon+1:]
	}

	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	return
}

// validOptionalPort reports whether port is either an empty string
// or matches /^:\d*$/
func validOptionalPort(port string) bool {
	if port == "" {
		return true
	}
	if port[0] != ':' {
		return false
	}
	for _, b := range port[1:] {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func must2[T1 any, T2 any](v1 T1, v2 T2, err error) (T1, T2) {
	if err != nil {
		panic(err)
	}
	return v1, v2
}
