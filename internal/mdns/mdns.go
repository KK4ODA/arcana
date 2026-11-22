// Package mdns is a small multicast DNS responder (RFC 6762) with DNS-SD
// service advertisement (RFC 6763). It lets devices on the local network
// open Arcana as http://arcana.local:8470/ and lets service browsers find it.
//
// It answers only for its own host name and service; it never resolves
// other names. It shares UDP port 5353 with the operating system's responder
// (Avahi, Bonjour, Windows) by using address reuse.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

const (
	hostTTL    = 120
	serviceTTL = 4500
	unicastBit = 1 << 15 // QU in questions, cache-flush in answers
	serviceSRV = "_http._tcp.local."
	enumName   = "_services._dns-sd._udp.local."
)

// Service is an additional DNS-SD service type to advertise on the same
// port (for example "_arcana._tcp" for the integration API).
type Service struct {
	Type string // "_name._tcp"
	TXT  []string
}

// Config describes what to advertise.
type Config struct {
	// Name is the host label (default "arcana"); the responder answers
	// for Name.local, or Name-2.local and so on after a conflict.
	Name string
	// Instance is the human-readable service name, e.g. "Arcana library".
	Instance string
	Port     int
	// TXT is the TXT record of the _http._tcp service.
	TXT   []string
	Extra []Service
	Log   *slog.Logger
}

type service struct {
	typ      string // "_http._tcp.local."
	instance string // "Arcana library._http._tcp.local."
	txt      []string
}

// Responder answers mDNS queries.
type Responder struct {
	cfg Config

	mu       sync.Mutex
	host     string // e.g. "arcana.local."
	services []service
	own      map[string]bool
}

// New prepares a responder.
func New(cfg Config) *Responder {
	if cfg.Name == "" {
		cfg.Name = "arcana"
	}
	if cfg.Instance == "" {
		cfg.Instance = "Arcana"
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	r := &Responder{cfg: cfg, own: map[string]bool{}}
	r.setName(cfg.Name)
	return r
}

// SanitizeLabel turns text into a valid lowercase host label.
func SanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '-' || c == ' ' || c == '_' || c == '.':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}

func (r *Responder) setName(label string) {
	inst := strings.NewReplacer(".", " ", "\\", " ").Replace(r.cfg.Instance)
	if len(inst) > 63 {
		inst = inst[:63]
	}
	svcs := []service{{typ: serviceSRV, instance: inst + "." + serviceSRV, txt: r.cfg.TXT}}
	for _, e := range r.cfg.Extra {
		t := strings.TrimSuffix(e.Type, ".") + ".local."
		svcs = append(svcs, service{typ: t, instance: inst + "." + t, txt: e.TXT})
	}
	for i := range svcs {
		if len(svcs[i].txt) == 0 {
			svcs[i].txt = []string{""}
		}
	}
	r.mu.Lock()
	r.host = label + ".local."
	r.services = svcs
	r.mu.Unlock()
}

// Host returns the advertised host name without the trailing dot.
func (r *Responder) Host() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSuffix(r.host, ".")
}

func (r *Responder) names() (string, []service) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.host, append([]service(nil), r.services...)
}

func mustName(s string) dnsmessage.Name {
	n, err := dnsmessage.NewName(s)
	if err != nil {
		panic(err)
	}
	return n
}

// respond builds the answer to one query packet, or nil if it is not for
// us. addrs are the IPv4 addresses to announce on the receiving interface.
func (r *Responder) respond(packet []byte, addrs []net.IP, legacy bool) ([]byte, bool, error) {
	var p dnsmessage.Parser
	hdr, err := p.Start(packet)
	if err != nil {
		return nil, false, err
	}
	if hdr.Response || hdr.OpCode != 0 {
		return nil, false, nil
	}
	qs, err := p.AllQuestions()
	if err != nil {
		return nil, false, err
	}
	host, svcs := r.names()
	var wantA, wantEnum, unicast bool
	wantPTR := map[int]bool{}
	wantSRV := map[int]bool{}
	wantTXT := map[int]bool{}
	var answered []dnsmessage.Question
	for _, q := range qs {
		if uint16(q.Class)&unicastBit != 0 {
			unicast = true
		}
		name := strings.ToLower(q.Name.String())
		any := q.Type == dnsmessage.TypeALL
		hit := false
		if name == strings.ToLower(host) && (q.Type == dnsmessage.TypeA || any) {
			wantA, hit = true, true
		}
		if name == enumName && (q.Type == dnsmessage.TypePTR || any) {
			wantEnum, hit = true, true
		}
		for i, sv := range svcs {
			if name == strings.ToLower(sv.typ) && (q.Type == dnsmessage.TypePTR || any) {
				wantPTR[i], hit = true, true
			}
			if name == strings.ToLower(sv.instance) {
				if q.Type == dnsmessage.TypeSRV || any {
					wantSRV[i], hit = true, true
				}
				if q.Type == dnsmessage.TypeTXT || any {
					wantTXT[i], hit = true, true
				}
			}
		}
		if hit {
			answered = append(answered, q)
		}
	}
	if len(answered) == 0 || (wantA && len(addrs) == 0 && len(wantPTR)+len(wantSRV)+len(wantTXT) == 0 && !wantEnum) {
		return nil, false, nil
	}
	h := dnsmessage.Header{Response: true, Authoritative: true}
	if legacy {
		h.ID = hdr.ID
	}
	b := dnsmessage.NewBuilder(nil, h)
	b.EnableCompression()
	if legacy {
		// Legacy unicast replies repeat the question (RFC 6762 §6.7).
		if err := b.StartQuestions(); err != nil {
			return nil, false, err
		}
		for _, q := range answered {
			q.Class &^= unicastBit
			if err := b.Question(q); err != nil {
				return nil, false, err
			}
		}
	}
	if err := b.StartAnswers(); err != nil {
		return nil, false, err
	}
	flush := dnsmessage.Class(dnsmessage.ClassINET)
	if !legacy {
		flush |= unicastBit
	}
	ttl := func(t uint32) uint32 {
		if legacy && t > 10 {
			return 10
		}
		return t
	}
	hostName := mustName(host)
	addA := func() error {
		for _, ip := range addrs {
			var a [4]byte
			copy(a[:], ip.To4())
			if err := b.AResource(dnsmessage.ResourceHeader{Name: hostName, Class: flush, TTL: ttl(hostTTL)}, dnsmessage.AResource{A: a}); err != nil {
				return err
			}
		}
		return nil
	}
	srv := func(sv service) error {
		return b.SRVResource(dnsmessage.ResourceHeader{Name: mustName(sv.instance), Class: flush, TTL: ttl(hostTTL)},
			dnsmessage.SRVResource{Target: hostName, Port: uint16(r.cfg.Port)})
	}
	txt := func(sv service) error {
		return b.TXTResource(dnsmessage.ResourceHeader{Name: mustName(sv.instance), Class: flush, TTL: ttl(serviceTTL)}, dnsmessage.TXTResource{TXT: sv.txt})
	}
	if wantA {
		if err := addA(); err != nil {
			return nil, false, err
		}
	}
	for i, sv := range svcs {
		if wantPTR[i] {
			if err := b.PTRResource(dnsmessage.ResourceHeader{Name: mustName(sv.typ), Class: dnsmessage.ClassINET, TTL: ttl(serviceTTL)},
				dnsmessage.PTRResource{PTR: mustName(sv.instance)}); err != nil {
				return nil, false, err
			}
		}
	}
	if wantEnum {
		for _, sv := range svcs {
			if err := b.PTRResource(dnsmessage.ResourceHeader{Name: mustName(enumName), Class: dnsmessage.ClassINET, TTL: ttl(serviceTTL)},
				dnsmessage.PTRResource{PTR: mustName(sv.typ)}); err != nil {
				return nil, false, err
			}
		}
	}
	for i, sv := range svcs {
		if wantSRV[i] {
			if err := srv(sv); err != nil {
				return nil, false, err
			}
		}
		if wantTXT[i] {
			if err := txt(sv); err != nil {
				return nil, false, err
			}
		}
	}
	// Additional records save the browser a round trip (RFC 6763 §12).
	if err := b.StartAdditionals(); err != nil {
		return nil, false, err
	}
	for i, sv := range svcs {
		if wantPTR[i] && !wantSRV[i] {
			if err := srv(sv); err != nil {
				return nil, false, err
			}
			if err := txt(sv); err != nil {
				return nil, false, err
			}
		}
	}
	if (len(wantPTR) > 0 || len(wantSRV) > 0) && !wantA {
		if err := addA(); err != nil {
			return nil, false, err
		}
	}
	out, err := b.Finish()
	return out, unicast, err
}

// announcement is an unsolicited response with every record (ttl 0 for
// goodbye).
func (r *Responder) announcement(addrs []net.IP, goodbye bool) ([]byte, error) {
	host, svcs := r.names()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.EnableCompression()
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	t := func(v uint32) uint32 {
		if goodbye {
			return 0
		}
		return v
	}
	flush := dnsmessage.Class(dnsmessage.ClassINET | unicastBit)
	hostName := mustName(host)
	for _, ip := range addrs {
		var a [4]byte
		copy(a[:], ip.To4())
		if err := b.AResource(dnsmessage.ResourceHeader{Name: hostName, Class: flush, TTL: t(hostTTL)}, dnsmessage.AResource{A: a}); err != nil {
			return nil, err
		}
	}
	for _, sv := range svcs {
		instName := mustName(sv.instance)
		if err := b.PTRResource(dnsmessage.ResourceHeader{Name: mustName(sv.typ), Class: dnsmessage.ClassINET, TTL: t(serviceTTL)},
			dnsmessage.PTRResource{PTR: instName}); err != nil {
			return nil, err
		}
		if err := b.SRVResource(dnsmessage.ResourceHeader{Name: instName, Class: flush, TTL: t(hostTTL)},
			dnsmessage.SRVResource{Target: hostName, Port: uint16(r.cfg.Port)}); err != nil {
			return nil, err
		}
		if err := b.TXTResource(dnsmessage.ResourceHeader{Name: instName, Class: flush, TTL: t(serviceTTL)}, dnsmessage.TXTResource{TXT: sv.txt}); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

// conflicts reports whether a response claims our host name with
// addresses that are not ours.
func (r *Responder) conflicts(packet []byte, own map[string]bool) bool {
	var p dnsmessage.Parser
	hdr, err := p.Start(packet)
	if err != nil || !hdr.Response {
		return false
	}
	if err := p.SkipAllQuestions(); err != nil {
		return false
	}
	host, _ := r.names()
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			return false
		}
		if h.Type == dnsmessage.TypeA && strings.EqualFold(h.Name.String(), host) {
			a, err := p.AResource()
			if err != nil {
				return false
			}
			if !own[net.IP(a.A[:]).String()] {
				return true
			}
			continue
		}
		if err := p.SkipAnswer(); err != nil {
			return false
		}
	}
}

// probeQuery asks who owns our host name.
func (r *Responder) probeQuery() []byte {
	host, _ := r.names()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: mustName(host), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	out, _ := b.Finish()
	return out
}

type iface struct {
	ifi   net.Interface
	addrs []net.IP
}

func interfaces() []iface {
	var out []iface
	list, _ := net.Interfaces()
	for _, ifi := range list {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		var ips []net.IP
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				ips = append(ips, ipn.IP.To4())
			}
		}
		if len(ips) > 0 {
			out = append(out, iface{ifi, ips})
		}
	}
	return out
}

// Run serves until ctx ends.
func (r *Responder) Run(ctx context.Context) error {
	lc := net.ListenConfig{Control: reuseControl}
	pc, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:5353")
	if err != nil {
		return fmt.Errorf("mDNS: %w", err)
	}
	defer pc.Close()
	p := ipv4.NewPacketConn(pc)
	_ = p.SetControlMessage(ipv4.FlagInterface, true)
	_ = p.SetMulticastTTL(255)
	_ = p.SetMulticastLoopback(true)

	var ifs []iface
	joined := map[int]bool{}
	refresh := func() {
		ifs = interfaces()
		own := map[string]bool{}
		for _, f := range ifs {
			for _, ip := range f.addrs {
				own[ip.String()] = true
			}
			if !joined[f.ifi.Index] {
				ifi := f.ifi
				if err := p.JoinGroup(&ifi, group); err == nil {
					joined[f.ifi.Index] = true
				}
			}
		}
		r.mu.Lock()
		r.own = own
		r.mu.Unlock()
	}
	refresh()
	if len(joined) == 0 {
		return errors.New("mDNS: no network interface supports multicast")
	}
	send := func(b []byte, ifIndex int, to *net.UDPAddr) {
		_, _ = p.WriteTo(b, &ipv4.ControlMessage{IfIndex: ifIndex}, to)
	}
	announce := func(goodbye bool) {
		for _, f := range ifs {
			if msg, err := r.announcement(f.addrs, goodbye); err == nil {
				send(msg, f.ifi.Index, group)
			}
		}
	}
	addrsFor := func(index int) []net.IP {
		var all []net.IP
		for _, f := range ifs {
			if f.ifi.Index == index {
				return f.addrs
			}
			all = append(all, f.addrs...)
		}
		// A unicast query on loopback or an interface we did not join:
		// answer with every address.
		return all
	}

	// Reader goroutine feeds packets to the loop below.
	type pkt struct {
		data []byte
		idx  int
		from *net.UDPAddr
	}
	pkts := make(chan pkt, 32)
	go func() {
		buf := make([]byte, 9000)
		for {
			n, cm, from, err := p.ReadFrom(buf)
			if err != nil {
				close(pkts)
				return
			}
			idx := 0
			if cm != nil {
				idx = cm.IfIndex
			}
			ua, _ := from.(*net.UDPAddr)
			select {
			case pkts <- pkt{append([]byte(nil), buf[:n]...), idx, ua}:
			default: // drop under load
			}
		}
	}()

	// Probe for conflicts: ask three times, 250 ms apart.
	base := r.cfg.Name
	for attempt := 2; attempt <= 10; attempt++ {
		conflict := false
		for i := 0; i < 3 && !conflict; i++ {
			for _, f := range ifs {
				send(r.probeQuery(), f.ifi.Index, group)
			}
			deadline := time.After(250 * time.Millisecond)
		wait:
			for {
				select {
				case <-ctx.Done():
					return nil
				case k, ok := <-pkts:
					if !ok {
						return errors.New("mDNS socket closed")
					}
					r.mu.Lock()
					own := r.own
					r.mu.Unlock()
					if r.conflicts(k.data, own) {
						conflict = true
						break wait
					}
				case <-deadline:
					break wait
				}
			}
		}
		if !conflict {
			break
		}
		r.setName(fmt.Sprintf("%s-%d", base, attempt))
		r.cfg.Log.Info("mDNS name in use; trying another", "name", r.Host())
	}
	r.cfg.Log.Info("mDNS: advertising", "host", r.Host(), "port", r.cfg.Port)
	announce(false)
	reannounce := time.After(time.Second)
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	defer announce(true)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reannounce:
			announce(false)
		case <-tick.C:
			before := len(joined)
			refresh()
			if len(joined) != before {
				announce(false)
			}
		case k, ok := <-pkts:
			if !ok {
				return errors.New("mDNS socket closed")
			}
			if k.from == nil {
				continue
			}
			legacy := k.from.Port != 5353
			reply, unicast, err := r.respond(k.data, addrsFor(k.idx), legacy)
			if err != nil || reply == nil {
				continue
			}
			if legacy || unicast {
				send(reply, k.idx, k.from)
			} else {
				send(reply, k.idx, group)
			}
		}
	}
}
