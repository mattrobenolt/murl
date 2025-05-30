package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"

	"github.com/quic-go/quic-go/http3"
)

func handle(w http.ResponseWriter, r *http.Request) {
	fmt.Println(string(must(httputil.DumpRequest(r, true))))
}

func main() {
	panic(http3.ListenAndServeQUIC("127.0.0.1:8443", "cmd/h3server/cert.pem", "cmd/h3server/key.pem", http.HandlerFunc(handle)))
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
