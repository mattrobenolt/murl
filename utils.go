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

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
