package main

import (
	"fmt"
	"os"

	flag "github.com/spf13/pflag"
)

var program = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

var (
	flagCompressed = program.Bool("compressed", false, "compressed")
	flagData       = program.StringP("data", "d", "", "data")
	flagFail       = program.BoolP("fail", "f", false, "fail on HTTP errors")
	flagHTTP1      = program.Bool("http1", true, "http1.1")
	flagHTTP2      = program.Bool("http2", true, "http2")
	flagHTTP2Prior = program.Bool("http2-prior-knowledge", false, "http2 with prior knowledge")
	flagHTTP3      = program.Bool("http3", true, "http3")
	flagHTTP3Only  = program.Bool("http3-only", false, "http3 only")
	flagHeader     = program.StringArrayP("header", "H", nil, "header")
	flagHelp       = program.BoolP("help", "h", false, "help")
	flagInsecure   = program.BoolP("insecure", "k", false, "insecure")
	flagJSON       = program.String("json", "", "json")
	flagLocation   = program.BoolP("location", "L", false, "follow redirects")
	flagMethod     = program.StringP("request", "X", "", "request method")
	flagOutput     = program.StringP("output", "o", "", "output")
	flagSilent     = program.BoolP("silent", "s", false, "silent mode")
	flagUser       = program.StringP("user", "u", "", "<user:password>")
	flagVerbose    = program.BoolP("verbose", "v", false, "verbose")
)

func init() {
	program.Usage = func() {
		fmt.Fprintf(program.Output(), "Usage: %s [options...] <url>\n", os.Args[0])
		program.PrintDefaults()
	}
	program.Parse(os.Args[1:])
}
