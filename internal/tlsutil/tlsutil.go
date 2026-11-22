// Package tlsutil provides Arcana's optional HTTPS: a self-signed
// certificate kept in the library, or a certificate the user supplies, and
// a listener that accepts both HTTP and HTTPS on one port.
package tlsutil

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Info describes the certificate in use.
type Info struct {
	Mode        string    `json:"mode"` // self-signed or custom
	Subject     string    `json:"subject"`
	Names       []string  `json:"names"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"` // SHA-256, colon-separated
}

// Fingerprint formats a certificate's SHA-256 fingerprint.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

func info(mode string, c *x509.Certificate) Info {
	names := append([]string(nil), c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	return Info{Mode: mode, Subject: c.Subject.CommonName, Names: names, NotAfter: c.NotAfter, Fingerprint: Fingerprint(c.Raw)}
}

// covers reports whether the certificate lists every name and address.
func covers(c *x509.Certificate, names []string, ips []net.IP) bool {
	have := map[string]bool{}
	for _, n := range c.DNSNames {
		have[strings.ToLower(n)] = true
	}
	for _, ip := range c.IPAddresses {
		have[ip.String()] = true
	}
	for _, n := range names {
		if !have[strings.ToLower(n)] {
			return false
		}
	}
	for _, ip := range ips {
		if !have[ip.String()] {
			return false
		}
	}
	return true
}

// SelfSigned loads dir/cert.pem and dir/key.pem, creating or renewing them
// when they are missing, expire within 30 days, or do not cover the given
// names and addresses.
func SelfSigned(dir string, names []string, ips []net.IP) (tls.Certificate, Info, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if pair, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if c, err := x509.ParseCertificate(pair.Certificate[0]); err == nil &&
			time.Until(c.NotAfter) > 30*24*time.Hour && covers(c, names, ips) {
			return pair, info("self-signed", c), nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, Info{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Arcana (self-signed)", Organization: []string{"Arcana offline library"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(397 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              names,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, Info{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, Info{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, Info{}, err
	}
	if err := fsutil.WriteFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, Info{}, err
	}
	if err := fsutil.WriteFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, Info{}, err
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, Info{}, err
	}
	c, _ := x509.ParseCertificate(der)
	return pair, info("self-signed", c), nil
}

// Custom loads a user-provided certificate and key (PEM).
func Custom(certPath, keyPath string) (tls.Certificate, Info, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, Info{}, fmt.Errorf("load %s and %s: %w", filepath.Base(certPath), filepath.Base(keyPath), err)
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, Info{}, err
	}
	if time.Now().After(c.NotAfter) {
		return tls.Certificate{}, Info{}, errors.New("the certificate has expired")
	}
	return pair, info("custom", c), nil
}

// ServerConfig returns a TLS configuration for a certificate.
func ServerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
}

// ---------------------------------------------------------------- listener

// Sniffer accepts HTTP and HTTPS on one port: a connection whose first byte
// is a TLS handshake record (0x16) is served over TLS when a configuration
// is available; anything else is plain HTTP.
type Sniffer struct {
	net.Listener
	// Config returns the current TLS configuration, or nil for HTTP only.
	Config func() *tls.Config

	once  sync.Once
	conns chan net.Conn
	errc  chan error
	done  chan struct{}
}

// NewSniffer wraps l.
func NewSniffer(l net.Listener, cfg func() *tls.Config) *Sniffer {
	return &Sniffer{Listener: l, Config: cfg, conns: make(chan net.Conn), errc: make(chan error, 1), done: make(chan struct{})}
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func (s *Sniffer) loop() {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			s.errc <- err
			return
		}
		go func() {
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			br := bufio.NewReader(c)
			first, err := br.Peek(1)
			_ = c.SetReadDeadline(time.Time{})
			if err != nil {
				c.Close()
				return
			}
			var out net.Conn = &peekedConn{Conn: c, r: br}
			if first[0] == 0x16 {
				cfg := s.Config()
				if cfg == nil {
					c.Close()
					return
				}
				out = tls.Server(out, cfg)
			}
			select {
			case s.conns <- out:
			case <-s.done:
				c.Close()
			}
		}()
	}
}

// Accept returns the next connection.
func (s *Sniffer) Accept() (net.Conn, error) {
	s.once.Do(func() { go s.loop() })
	select {
	case c := <-s.conns:
		return c, nil
	case err := <-s.errc:
		s.errc <- err
		return nil, err
	}
}

// Close stops the listener.
func (s *Sniffer) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return s.Listener.Close()
}
