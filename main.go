package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

var stderr = bufio.NewWriter(os.Stderr)

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error {
	return nil
}

func openOutput() (io.WriteCloser, bool) {
	switch *flagOutput {
	case "":
		return nopCloser{os.Stdout}, true
	case "-", "/dev/stdout":
		return nopCloser{os.Stdout}, false
	case "/dev/stderr":
		return nopCloser{os.Stderr}, false
	case "/dev/null":
		return nopCloser{io.Discard}, false
	}
	return must(os.Create(*flagOutput)), false
}

func main() {
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var tr http.RoundTripper
	if *flagHTTP3Only {
		tr = http3Transport()
	} else if *flagHTTP3 {
		tr = bothTransport()
	} else {
		tr = httpTransport()
	}

	headers := make(http.Header)
	headers.Set("Accept", "*/*")
	headers.Set("User-Agent", fmt.Sprintf("%s/1.0", program.Name()))

	if *flagCompressed {
		headers.Set("Accept-Encoding", "gzip, zstd, br")
	}

	if *flagJSON != "" {
		*flagData = *flagJSON
		headers.Set("Content-Type", "application/json")
		headers.Set("Accept", "application/json")
	}

	if *flagUser != "" {
		username, password, found := strings.Cut(*flagUser, ":")
		if !found {
			panic("invalid username:password")
		}
		headers.Set(
			"Authorization",
			"Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
		)
	}

	for _, header := range *flagHeader {
		key, value, found := strings.Cut(header, ":")
		if !found {
			panic("invalid header")
		}
		headers.Set(key, strings.TrimSpace(value))
	}

	out, onlyPrintable := openOutput()
	defer out.Close()

	method := strings.ToUpper(*flagMethod)

	var contentLength int64
	var body io.ReadCloser

	var stringReader *strings.Reader
	if *flagData != "" {
		stringReader = strings.NewReader(*flagData)
		body = io.NopCloser(stringReader)
		contentLength = int64(len(*flagData))
		if *flagMethod == "" {
			method = "POST"
		}
	}

	if method == "" {
		method = "GET"
	}

	for i := 0; i < len(urls); {
		url := urls[i]
		u := must(parseURL(url))

		if stringReader != nil {
			stringReader.Seek(0, io.SeekStart)
		}

		req := &http.Request{
			Method:        method,
			URL:           u,
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        headers,
			Body:          body,
			Host:          u.Host,
			ContentLength: contentLength,
		}

		if *flagHTTP3Only {
			req.ProtoMajor = 3
			req.ProtoMinor = 0
		} else if *flagHTTP2Prior {
			req.ProtoMajor = 2
			req.ProtoMinor = 0
		}

		dumpRequest(req)

		resp := must(tr.RoundTrip(req.WithContext(ctx)))

		dumpResponseHeaders(resp)

		if *flagLocation {
			switch resp.StatusCode {
			case 301, 302, 307, 308:
				io.Copy(io.Discard, resp.Body)
				urls[i] = resp.Header.Get("Location")
				if *flagVerbose {
					fmt.Fprintf(stderr, "* Redirecting to %q\n\n", urls[i])
					stderr.Flush()
				}
				continue
			}
		}

		if *flagFail && resp.StatusCode >= 300 {
			io.Copy(io.Discard, resp.Body)
			if !*flagSilent {
				fmt.Fprintf(stderr, "The requested URL returned error: %d\n", resp.StatusCode)
				stderr.Flush()
			}
			os.Exit(1)
		}

		dumpResponse(out, resp, onlyPrintable)
		i++
	}
}

func dumpRequest(r *http.Request) {
	if *flagVerbose {
		if r.ProtoMajor == 1 {
			fmt.Fprintf(stderr, "> %s %s HTTP/%d.%d\r\n", r.Method, r.URL.Path, r.ProtoMajor, r.ProtoMinor)
		} else {
			fmt.Fprintf(stderr, "> %s %s HTTP/%d\r\n", r.Method, r.URL.Path, r.ProtoMajor)
		}
		headers := sortHeaders(r.Header)
		stderr.WriteString("> Host: ")
		stderr.WriteString(r.Host)
		stderr.WriteString("\r\n")

		if r.ContentLength > 0 {
			fmt.Fprintf(stderr, "> Content-Length: %d\r\n", r.ContentLength)
		}

		for _, kvs := range headers {
			for _, v := range kvs.values {
				stderr.WriteString("> ")
				stderr.WriteString(kvs.key)
				stderr.WriteString(": ")
				stderr.WriteString(v)
				stderr.WriteString("\r\n")
			}
		}
		stderr.WriteString("> \r\n")
		if r.ContentLength > 0 {
			fmt.Fprintf(stderr, "* upload completely sent off: %d bytes\n", r.ContentLength)
		} else {
			stderr.WriteString("* Request completely sent off\n")
		}
		stderr.Flush()
	}
}

func dumpResponseHeaders(r *http.Response) {
	if *flagVerbose {
		if r.ProtoMajor == 1 {
			fmt.Fprintf(stderr, "< HTTP/%d.%d %s\r\n", r.ProtoMajor, r.ProtoMinor, r.Status)
		} else {
			fmt.Fprintf(stderr, "< HTTP/%d %s\r\n", r.ProtoMajor, r.Status)
		}
		headers := sortHeaders(r.Header)
		for _, kvs := range headers {
			for _, v := range kvs.values {
				stderr.WriteString("< ")
				stderr.WriteString(kvs.key)
				stderr.WriteString(": ")
				stderr.WriteString(v)
				stderr.WriteString("\r\n")
			}
		}
		stderr.WriteString("< \r\n")
		stderr.Flush()
	}
}

func dumpResponse(out io.Writer, r *http.Response, onlyPrintable bool) {
	defer r.Body.Close()

	var body io.Reader = r.Body
	if *flagCompressed {
		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			zreader := must(gzip.NewReader(r.Body))
			defer zreader.Close()
			body = zreader
		case "zstd":
			zreader := must(zstd.NewReader(r.Body))
			defer zreader.Close()
			body = zreader
		case "br":
			body = brotli.NewReader(r.Body)
		}
	}

	var buf [32 * 1024]byte
	must(copyBuffer(out, body, buf[:], onlyPrintable))
}
