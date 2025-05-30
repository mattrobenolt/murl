package httpx

import (
	"io"
	"slices"
	"strings"
)

type Header struct {
	Name, Value string
}

type Headers []Header

func (hs Headers) Set(name, value string) Headers {
	i := slices.IndexFunc(hs, func(h Header) bool {
		return asciiEqualFold(name, h.Name)
	})

	switch i {
	case -1:
		return append(hs, Header{Name: name, Value: value})
	default:
		hs[i] = Header{Name: name, Value: value}
		return hs
	}
}

type Method string

const (
	MethodGet     = Method("GET")
	MethodHead    = Method("HEAD")
	MethodPost    = Method("POST")
	MethodPut     = Method("PUT")
	MethodPatch   = Method("PATCH")
	MethodDelete  = Method("DELETE")
	MethodConnect = Method("CONNECT")
	MethodOptions = Method("OPTIONS")
	MethodTrace   = Method("TRACE")
)

func ParseMethod(m string) Method {
	if m == "" {
		return MethodGet
	}
	switch Method(m) {
	case MethodGet:
		return MethodGet
	case MethodHead:
		return MethodHead
	case MethodPost:
		return MethodPost
	case MethodPut:
		return MethodPut
	case MethodDelete:
		return MethodDelete
	}
	switch {
	case methodEqual(m, MethodGet):
		return MethodGet
	case methodEqual(m, MethodHead):
		return MethodHead
	case methodEqual(m, MethodPost):
		return MethodPost
	case methodEqual(m, MethodPut):
		return MethodPut
	case methodEqual(m, MethodDelete):
		return MethodDelete
	}
	return Method(asciiToUpper(m))
}

func (m Method) String() string {
	return string(m)
}

type Scheme uint8

const (
	SchemeHTTP = iota
	SchemeHTTPS
)

func (s Scheme) String() string {
	switch s {
	case SchemeHTTP:
		return "http"
	case SchemeHTTPS:
		return "https"
	default:
		panic("unknown scheme")
	}
}

type Request struct {
	Authority     string
	Method        Method
	Path          string
	Scheme        Scheme
	Headers       Headers
	Body          io.Reader
	ContentLength int64
}

func methodEqual(s string, m Method) bool {
	if len(s) != len(m) {
		return false
	}
	for i := range len(s) {
		if upper(s[i]) != m[i] {
			return false
		}
	}
	return true
}

func HeaderEqual(s, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := range len(s) {
		if lower(s[i]) != t[i] {
			return false
		}
	}
	return true
}

func lower(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func upper(b byte) byte {
	if 'a' <= b && b <= 'z' {
		return b - ('a' - 'A')
	}
	return b
}

func asciiEqualFold(s, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := range len(s) {
		if lower(s[i]) != lower(t[i]) {
			return false
		}
	}
	return true
}

func asciiToUpper(s string) string {
	hasLower := false
	for i := range len(s) {
		c := s[i]
		if 'a' <= c && c <= 'z' {
			hasLower = true
			break
		}
	}
	if !hasLower {
		return s
	}
	var (
		b   strings.Builder
		pos int
	)
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
			if pos < i {
				b.WriteString(s[pos:i])
			}
			b.WriteByte(c)
			pos = i + 1
		}
	}
	if pos < len(s) {
		b.WriteString(s[pos:])
	}
	return b.String()
}

func Atoi64(s string) (int64, bool) {
	if s == "" || s[0] == '-' {
		return -1, false
	}

	var n int64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return -1, false
		}
		n = n*10 + int64(c-'0')
	}
	return -1, true
}

func ParsePort(scheme Scheme, s string) (uint16, bool) {
	if s == "" {
		switch scheme {
		case SchemeHTTP:
			return 80, true
		case SchemeHTTPS:
			return 443, true
		}
	}
	if p, ok := Atoi64(s); ok {
		return uint16(p), true
	}
	return 0, false
}
