package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"rsc.io/qr"
)

// qrSVG renders text as a QR code (error correction M) with the standard
// four-module quiet zone, black on white so it prints and scans reliably
// in either theme.
func qrSVG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	n := code.Size + 8
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.Bytes(), nil
}

// handleQR draws a QR code for one of this server's own addresses. It does
// not encode arbitrary text, so it cannot be used to make codes pointing
// elsewhere.
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	u, err := url.Parse(target)
	if err != nil || len(target) > 200 || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		!s.policy.hostAllowed(u.Host) || (u.Port() != "" && u.Port() != itoa(s.appPort)) {
		writeError(w, http.StatusBadRequest, "Only this server's own addresses can be shown as a QR code.")
		return
	}
	svg, err := qrSVG(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(svg)
}

// wifiQRText formats Wi-Fi credentials in the widely supported
// "WIFI:" scheme, escaping special characters.
func wifiQRText(ssid, password string) string {
	esc := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `:`, `\:`, `"`, `\"`)
	if password == "" {
		return "WIFI:T:nopass;S:" + esc.Replace(ssid) + ";;"
	}
	return "WIFI:T:WPA;S:" + esc.Replace(ssid) + ";P:" + esc.Replace(password) + ";;"
}
