package mdns

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func query(t *testing.T, id uint16, qs ...dnsmessage.Question) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id})
	b.StartQuestions()
	for _, q := range qs {
		if err := b.Question(q); err != nil {
			t.Fatal(err)
		}
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func q(name string, typ dnsmessage.Type, class uint16) dnsmessage.Question {
	return dnsmessage.Question{Name: mustName(name), Type: typ, Class: dnsmessage.Class(class)}
}

type parsed struct {
	hdr         dnsmessage.Header
	questions   int
	answers     []dnsmessage.Resource
	additionals []dnsmessage.Resource
}

func parse(t *testing.T, b []byte) parsed {
	t.Helper()
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		t.Fatal(err)
	}
	return parsed{m.Header, len(m.Questions), m.Answers, m.Additionals}
}

func TestRespond(t *testing.T) {
	r := New(Config{Name: "arcana", Instance: "Arcana (Field kit)", Port: 8470, TXT: []string{"path=/"}})
	addrs := []net.IP{net.IPv4(192, 168, 1, 20)}

	// Host lookup, case-insensitive.
	out, unicast, err := r.respond(query(t, 0, q("ARCANA.local.", dnsmessage.TypeA, 1)), addrs, false)
	if err != nil || out == nil || unicast {
		t.Fatalf("A: %v %v", err, unicast)
	}
	m := parse(t, out)
	if !m.hdr.Response || !m.hdr.Authoritative || len(m.answers) != 1 || m.questions != 0 {
		t.Fatalf("A response %+v", m)
	}
	a := m.answers[0].Body.(*dnsmessage.AResource)
	if net.IP(a.A[:]).String() != "192.168.1.20" || m.answers[0].Header.Class&unicastBit == 0 || m.answers[0].Header.TTL != hostTTL {
		t.Fatalf("A record %+v", m.answers[0])
	}

	// Service browsing returns PTR with SRV, TXT and A as additionals.
	out, _, _ = r.respond(query(t, 0, q("_http._tcp.local.", dnsmessage.TypePTR, 1)), addrs, false)
	m = parse(t, out)
	ptr := m.answers[0].Body.(*dnsmessage.PTRResource)
	if ptr.PTR.String() != "Arcana (Field kit)._http._tcp.local." || len(m.additionals) != 3 {
		t.Fatalf("PTR %+v additionals %d", ptr, len(m.additionals))
	}
	srv := m.additionals[0].Body.(*dnsmessage.SRVResource)
	if srv.Port != 8470 || srv.Target.String() != "arcana.local." {
		t.Fatalf("SRV %+v", srv)
	}

	// QU bit requests a unicast reply.
	_, unicast, _ = r.respond(query(t, 0, q("arcana.local.", dnsmessage.TypeA, 1|unicastBit)), addrs, false)
	if !unicast {
		t.Fatal("QU ignored")
	}

	// Legacy unicast (from a port other than 5353): echo ID and question,
	// short TTL, no cache-flush bit.
	out, _, _ = r.respond(query(t, 4242, q("arcana.local.", dnsmessage.TypeA, 1)), addrs, true)
	m = parse(t, out)
	if m.hdr.ID != 4242 || m.questions != 1 || m.answers[0].Header.TTL != 10 || m.answers[0].Header.Class&unicastBit != 0 {
		t.Fatalf("legacy %+v", m)
	}

	// Other names, responses and garbage are ignored.
	for _, pkt := range [][]byte{
		query(t, 0, q("printer.local.", dnsmessage.TypeA, 1)),
		query(t, 0, q("arcana.local.", dnsmessage.TypeAAAA, 1)),
		{1, 2, 3},
	} {
		if out, _, _ := r.respond(pkt, addrs, false); out != nil {
			t.Fatalf("answered %v", pkt)
		}
	}
	resp, _ := r.announcement(addrs, false)
	if out, _, _ := r.respond(resp, addrs, false); out != nil {
		t.Fatal("answered a response")
	}
}

func TestConflictsAndNames(t *testing.T) {
	r := New(Config{Name: "arcana", Port: 8470})
	theirs, _ := New(Config{Name: "arcana"}).announcement([]net.IP{net.IPv4(10, 0, 0, 9)}, false)
	if !r.conflicts(theirs, map[string]bool{"10.0.0.5": true}) {
		t.Fatal("conflict not detected")
	}
	if r.conflicts(theirs, map[string]bool{"10.0.0.9": true}) {
		t.Fatal("own announcement treated as conflict")
	}
	other, _ := New(Config{Name: "kitchen"}).announcement([]net.IP{net.IPv4(10, 0, 0, 9)}, false)
	if r.conflicts(other, nil) {
		t.Fatal("different name treated as conflict")
	}
	bye, _ := r.announcement([]net.IP{net.IPv4(10, 0, 0, 5)}, true)
	if m := parse(t, bye); m.answers[0].Header.TTL != 0 {
		t.Fatal("goodbye must have TTL 0")
	}
	for in, want := range map[string]string{"Arcana": "arcana", "Field Kit 2": "field-kit-2", "ÄÖ--x..y": "x-y", "": ""} {
		if got := SanitizeLabel(in); got != want {
			t.Errorf("SanitizeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLiveResponder runs the responder on this machine and queries it with
// a legacy unicast query over loopback.
func TestLiveResponder(t *testing.T) {
	r := New(Config{Name: "arcana-test", Port: 8470})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- r.Run(ctx) }()
	defer func() { cancel(); <-errc }()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1500)
	for time.Now().Before(deadline) {
		select {
		case err := <-errc:
			t.Skipf("responder cannot run here: %v", err)
		default:
		}
		conn.WriteTo(query(t, 7, q("arcana-test.local.", dnsmessage.TypeA, 1)), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353})
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}
		m := parse(t, buf[:n])
		if m.hdr.ID != 7 || len(m.answers) == 0 {
			t.Fatalf("reply %+v", m)
		}
		t.Logf("arcana-test.local answered with %d address(es)", len(m.answers))
		return
	}
	t.Skip("no reply over loopback (port 5353 may be held exclusively by another responder)")
}

func TestExtraService(t *testing.T) {
	r := New(Config{Name: "arcana", Instance: "Arcana", Port: 8470,
		Extra: []Service{{Type: "_arcana._tcp", TXT: []string{"api=/api/integration/v1/"}}}})
	addrs := []net.IP{net.IPv4(10, 0, 0, 2)}
	out, _, err := r.respond(query(t, 0, q("_arcana._tcp.local.", dnsmessage.TypePTR, 1)), addrs, false)
	if err != nil || out == nil {
		t.Fatal(err)
	}
	m := parse(t, out)
	if ptr := m.answers[0].Body.(*dnsmessage.PTRResource); ptr.PTR.String() != "Arcana._arcana._tcp.local." {
		t.Fatalf("PTR %v", ptr.PTR)
	}
	var txt *dnsmessage.TXTResource
	for _, a := range m.additionals {
		if t, ok := a.Body.(*dnsmessage.TXTResource); ok {
			txt = t
		}
	}
	if txt == nil || txt.TXT[0] != "api=/api/integration/v1/" {
		t.Fatalf("TXT %+v", txt)
	}
	// Service enumeration lists both types.
	out, _, _ = r.respond(query(t, 0, q("_services._dns-sd._udp.local.", dnsmessage.TypePTR, 1)), addrs, false)
	if m := parse(t, out); len(m.answers) != 2 {
		t.Fatalf("enumeration %d", len(m.answers))
	}
	ann, _ := r.announcement(addrs, false)
	if m := parse(t, ann); len(m.answers) != 1+3*2 {
		t.Fatalf("announcement has %d records", len(m.answers))
	}
}
