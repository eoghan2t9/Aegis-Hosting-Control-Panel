package svc

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func TestAltSvcValue(t *testing.T) {
	got, err := altSvcValue(":443")
	if err != nil || got != `h3=":443"; ma=86400` {
		t.Errorf("got %q, %v", got, err)
	}
	if got, _ := altSvcValue("0.0.0.0:8443"); got != `h3=":8443"; ma=86400` {
		t.Errorf("custom port: %q", got)
	}
	if _, err := altSvcValue("nonsense"); err == nil {
		t.Error("bad address accepted")
	}
}

func TestWithAltSvcOnlyOnTCP(t *testing.T) {
	h := withAltSvc(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), `h3=":443"; ma=86400`)
	tcp := httptest.NewRecorder()
	h.ServeHTTP(tcp, httptest.NewRequest("GET", "https://a.test/", nil))
	if tcp.Header().Get("Alt-Svc") == "" {
		t.Error("HTTP/1.1 response lacks Alt-Svc")
	}
	quic := httptest.NewRequest("GET", "https://a.test/", nil)
	quic.ProtoMajor = 3
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, quic)
	if rec.Header().Get("Alt-Svc") != "" {
		t.Error("HTTP/3 response should not advertise itself")
	}
}

func TestHTTP3Toggle(t *testing.T) {
	t.Setenv("AEGIS_GO_HTTP3", "")
	if !http3Enabled() {
		t.Error("should default to on")
	}
	for _, v := range []string{"off", "0", "FALSE", " no "} {
		t.Setenv("AEGIS_GO_HTTP3", v)
		if http3Enabled() {
			t.Errorf("%q should disable", v)
		}
	}
}

// A real QUIC round trip: the server startHTTP3 builds must answer an HTTP/3
// client, with the certificate chosen through the same GetCertificate hook.
func TestStartHTTP3ServesRealClient(t *testing.T) {
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip("no UDP available:", err)
	}
	addr := probe.LocalAddr().String()
	probe.Close()

	cert, err := generateSelfSignedCert("h3.test")
	if err != nil {
		t.Fatal(err)
	}
	conf := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return cert, nil }, MinVersion: tls.VersionTLS12}
	srv, err := startHTTP3(addr, conf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "proto="+r.Proto+" host="+r.Host)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	tr := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "h3.test"}}
	defer tr.Close()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.ProtoMajor != 3 || string(body) != "proto=HTTP/3.0 host="+addr {
		t.Errorf("proto %d, body %q", resp.ProtoMajor, body)
	}
}
