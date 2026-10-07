package svc

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http/httptest"
	"testing"
)

// fakeFPM accepts one FastCGI request, returns its PARAMS and answers 200.
func fakeFPM(t *testing.T) (addr string, got <-chan map[string]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan map[string]string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		var raw bytes.Buffer
		for {
			rec, err := fcgiRead(conn, br)
			if err != nil {
				return
			}
			if rec.Type == fcgiParams {
				raw.Write(rec.Content)
			}
			if rec.Type == fcgiStdin && len(rec.Content) == 0 {
				break
			}
		}
		m := map[string]string{}
		b := raw.Bytes()
		readLen := func() int {
			if b[0] < 128 {
				n := int(b[0])
				b = b[1:]
				return n
			}
			n := int(b[0]&0x7f)<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
			b = b[4:]
			return n
		}
		for len(b) > 0 {
			nl, vl := readLen(), readLen()
			m[string(b[:nl])] = string(b[nl : nl+vl])
			b = b[nl+vl:]
		}
		ch <- m
		_ = fcgiWrite(conn, fcgiStdout, 1, []byte("Status: 200 OK\r\n\r\nok"))
		_ = fcgiWrite(conn, fcgiStdout, 1, nil)
		_ = fcgiWrite(conn, fcgiEndRequest, 1, make([]byte, 8))
	}()
	return "tcp:" + ln.Addr().String(), ch
}

// After an internal .htaccess rewrite, PHP must still see the browser's URL
// in REQUEST_URI (as under Apache), or index.php routers cannot dispatch.
func TestFcgiExecKeepsOriginalURIAfterRewrite(t *testing.T) {
	addr, got := fakeFPM(t)
	r := httptest.NewRequest("GET", "/app/api/ads?x=1", nil)
	r2 := r.Clone(context.WithValue(r.Context(), origURIKey{}, r.URL.RequestURI()))
	r2.URL.Path, r2.URL.RawQuery = "/app/api/index.php", ""
	w := &WebServer{}
	if _, _, _, _, err := w.fcgiExec(addr, r2, "/x/index.php", "/app/api/index.php"); err != nil {
		t.Fatal(err)
	}
	p := <-got
	if p["REQUEST_URI"] != "/app/api/ads?x=1" || p["REDIRECT_URL"] != "/app/api/ads" {
		t.Fatalf("REQUEST_URI=%q REDIRECT_URL=%q", p["REQUEST_URI"], p["REDIRECT_URL"])
	}
	if p["SCRIPT_NAME"] != "/app/api/index.php" {
		t.Fatalf("SCRIPT_NAME=%q", p["SCRIPT_NAME"])
	}
}

func TestFcgiExecPlainRequestURI(t *testing.T) {
	addr, got := fakeFPM(t)
	r := httptest.NewRequest("GET", "/a.php?y=2", nil)
	w := &WebServer{}
	if _, _, _, _, err := w.fcgiExec(addr, r, "/x/a.php", "/a.php"); err != nil {
		t.Fatal(err)
	}
	p := <-got
	if p["REQUEST_URI"] != "/a.php?y=2" {
		t.Fatalf("REQUEST_URI=%q", p["REQUEST_URI"])
	}
	if _, ok := p["REDIRECT_URL"]; ok {
		t.Fatal("REDIRECT_URL must only be set after a rewrite")
	}
}
