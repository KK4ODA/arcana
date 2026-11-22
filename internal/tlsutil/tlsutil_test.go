package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelfSignedAndSniffer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	names := []string{"localhost", "arcana.local"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1)}
	cert, inf, err := SelfSigned(dir, names, ips)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Mode != "self-signed" || len(inf.Fingerprint) != 95 || len(inf.Names) != 3 {
		t.Fatalf("info %+v", inf)
	}
	// Reused while it still covers the names...
	_, again, _ := SelfSigned(dir, names, ips)
	if again.Fingerprint != inf.Fingerprint {
		t.Fatal("certificate regenerated needlessly")
	}
	// ...and renewed when a new address appears.
	_, renewed, _ := SelfSigned(dir, names, append(ips, net.IPv4(192, 168, 7, 1)))
	if renewed.Fingerprint == inf.Fingerprint {
		t.Fatal("certificate not renewed for a new address")
	}
	if st, _ := os.Stat(filepath.Join(dir, "key.pem")); st == nil {
		t.Fatal("key not stored")
	}
	if _, _, err := Custom(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "missing.pem")); err == nil {
		t.Fatal("missing key accepted")
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var enabled atomic.Bool
	enabled.Store(true)
	sn := NewSniffer(l, func() *tls.Config {
		if !enabled.Load() {
			return nil
		}
		return ServerConfig(cert)
	})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			io.WriteString(w, "secure")
		} else {
			io.WriteString(w, "plain")
		}
	})}
	go srv.Serve(sn)
	defer srv.Close()
	addr := l.Addr().String()

	get := func(c *http.Client, url string) (string, error) {
		resp, err := c.Get(url)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	if body, err := get(http.DefaultClient, "http://"+addr+"/"); err != nil || body != "plain" {
		t.Fatalf("http: %q %v", body, err)
	}
	pool := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool.AddCert(leaf)
	tlsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "arcana.local"}}}
	if body, err := get(tlsClient, "https://"+addr+"/"); err != nil || body != "secure" {
		t.Fatalf("https: %q %v", body, err)
	}
	// With TLS turned off, HTTPS attempts are refused while HTTP still works.
	enabled.Store(false)
	tlsClient.CloseIdleConnections()
	if _, err := get(tlsClient, "https://"+addr+"/"); err == nil {
		t.Fatal("TLS served while disabled")
	}
	// A silent client does not block others.
	idle, _ := net.Dial("tcp", addr)
	defer idle.Close()
	done := make(chan string, 1)
	go func() { b, _ := get(http.DefaultClient, "http://"+addr+"/"); done <- b }()
	select {
	case b := <-done:
		if !strings.Contains(b, "plain") {
			t.Fatal(b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an idle connection blocked the listener")
	}
}
