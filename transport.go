package main

import (
	"cmp"
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func httpTransport() *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(*flagHTTP1)
	protocols.SetHTTP2(*flagHTTP2)
	protocols.SetUnencryptedHTTP2(*flagHTTP2Prior)

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   1,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		Protocols:             protocols,
		DisableCompression:    true,
	}
	return transport
}

func http3Transport() *http3.Transport {
	return &http3.Transport{
		QUICConfig: &quic.Config{
			HandshakeIdleTimeout: 30 * time.Second,
			MaxIdleTimeout:       30 * time.Second,
		},
		DisableCompression: true,
	}
}

type Transport struct {
	h2 *http.Transport
	h3 *http3.Transport
}

func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == "http" {
		return t.h2.RoundTrip(r)
	}

	ctx := r.Context()
	ctx1, cancel1 := context.WithCancel(ctx)
	ctx2, cancel2 := context.WithCancel(ctx)

	type result struct {
		r   *http.Response
		err error
	}

	var results [2]result
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		resp, err := t.h2.RoundTrip(r.WithContext(ctx1))
		results[0] = result{resp, err}
		if err == nil {
			cancel2()
		}
	}()

	go func() {
		defer wg.Done()
		resp, err := t.h3.RoundTrip(r.WithContext(ctx2))
		results[1] = result{resp, err}
		if err == nil {
			cancel1()
		}
	}()

	wg.Wait()

	if results[0].r != nil {
		return results[0].r, nil
	}
	if results[1].r != nil {
		return results[1].r, nil
	}
	return nil, cmp.Or(results[0].err, results[1].err)
}

func bothTransport() *Transport {
	return &Transport{
		h2: httpTransport(),
		h3: http3Transport(),
	}
}
