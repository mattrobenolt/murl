package main

import (
	"bufio"
	"io"
	"net/netip"
	"os"
	"strconv"
)

var stderr outputWriter

type outputWriter interface {
	WriteString(string) (int, error)
	WriteByte(byte) error
	Write([]byte) (int, error)
	WriteUint64(uint64) (int, error)
	WriteAddr(netip.Addr) (int, error)
	WriteAddrPort(netip.AddrPort) (int, error)
	Flush() error
}

type debugWriter struct {
	bufio.Writer
}

func (b *debugWriter) WriteUint64(i uint64) (int, error) {
	switch i {
	case 80:
		return b.WriteString("80")
	case 443:
		return b.WriteString("443")
	}
	return b.Write(strconv.AppendUint(b.AvailableBuffer(), i, 10))
}

func (b *debugWriter) WriteAddr(a netip.Addr) (int, error) {
	return b.Write(a.AppendTo(b.AvailableBuffer()))
}

func (b *debugWriter) WriteAddrPort(a netip.AddrPort) (int, error) {
	return b.Write(a.AppendTo(b.AvailableBuffer()))
}

type noopWriter struct{}

func (noopWriter) WriteString(string) (int, error)           { return 0, nil }
func (noopWriter) WriteByte(byte) error                      { return nil }
func (noopWriter) Write([]byte) (int, error)                 { return 0, nil }
func (noopWriter) WriteUint64(uint64) (int, error)           { return 0, nil }
func (noopWriter) WriteAddr(netip.Addr) (int, error)         { return 0, nil }
func (noopWriter) WriteAddrPort(netip.AddrPort) (int, error) { return 0, nil }
func (noopWriter) Flush() error                              { return nil }

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
