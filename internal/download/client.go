package download

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

var errBlockedAddress = errors.New("address is on a local or private network; enable \"Allow downloads from local network addresses\" in Settings to permit it")

// NewClient returns an HTTP client for catalog and content downloads.
//
// Catalog URLs are untrusted. Unless allowPrivate returns true, the client
// refuses to connect to loopback, private, link-local and other non-public
// addresses, checked on the IP actually dialed (so DNS rebinding cannot
// bypass it). A configured HTTP(S) proxy is exempt because it is chosen by
// the user, not by a catalog.
func NewClient(allowPrivate func() bool) *http.Client {
	return NewClientFor(allowPrivate, nil)
}

// NewClientFor is NewClient with an additional per-address exemption, used
// for peers on the local network that the user configured explicitly.
func NewClientFor(allowPrivate func() bool, allowAddr func(address string) bool) *http.Client {
	proxies := proxyHosts()
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	dialer.Control = func(network, address string, _ syscall.RawConn) error {
		if allowPrivate != nil && allowPrivate() {
			return nil
		}
		if allowAddr != nil && allowAddr(address) {
			return nil
		}
		if proxies[address] {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !isPublicIP(ip) {
			return fmt.Errorf("%s: %w", address, errBlockedAddress)
		}
		return nil
	}
	tr := &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			// Configured peers are on the local network: go direct.
			if allowAddr != nil {
				port := r.URL.Port()
				if port == "" {
					port = map[string]string{"https": "443"}[r.URL.Scheme]
					if port == "" {
						port = "80"
					}
				}
				if allowAddr(net.JoinHostPort(r.URL.Hostname(), port)) {
					return nil, nil
				}
			}
			return http.ProxyFromEnvironment(r)
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		// Byte offsets must refer to the stored file, not a compressed
		// transfer encoding.
		DisableCompression: true,
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// proxyHosts lists host:port pairs of configured proxies, resolved to IPs.
func proxyHosts() map[string]bool {
	out := map[string]bool{}
	for _, v := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		raw := os.Getenv(v)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		host, port := u.Hostname(), u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		out[net.JoinHostPort(host, port)] = true
		if addrs, err := net.LookupHost(host); err == nil {
			for _, a := range addrs {
				out[net.JoinHostPort(a, port)] = true
			}
		}
	}
	return out
}

func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 carrier-grade NAT, 192.0.0.0/24, 198.18.0.0/15
		// benchmarking, 0.0.0.0/8.
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64,
			v4[0] == 192 && v4[1] == 0 && v4[2] == 0,
			v4[0] == 198 && (v4[1] == 18 || v4[1] == 19),
			v4[0] == 0:
			return false
		}
	}
	return !strings.HasPrefix(ip.String(), "fc") && !strings.HasPrefix(ip.String(), "fd")
}
