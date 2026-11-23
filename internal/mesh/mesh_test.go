package mesh

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"testing"
	"time"
)

func appendFixed32(b []byte, num int, v uint32) []byte {
	b = appendKey(b, num, 5)
	return binary.LittleEndian.AppendUint32(b, v)
}

func appendUint(b []byte, num int, v uint64) []byte { return appendVarint(appendKey(b, num, 0), v) }

func position(lat, lon float64, alt int32) []byte {
	var b []byte
	b = appendFixed32(b, 1, uint32(int32(math.Round(lat*1e7))))
	b = appendFixed32(b, 2, uint32(int32(math.Round(lon*1e7))))
	b = appendUint(b, 3, uint64(uint32(alt)))
	return b
}

func user(id, long, short string) []byte {
	var b []byte
	b = appendBytes(b, 1, []byte(id))
	b = appendBytes(b, 2, []byte(long))
	b = appendBytes(b, 3, []byte(short))
	return appendUint(b, 6, 1)
}

func encNodeInfo(num uint32, u, pos []byte, lastHeard uint32, hops uint64) []byte {
	b := appendUint(nil, 1, uint64(num))
	if u != nil {
		b = appendBytes(b, 2, u)
	}
	if pos != nil {
		b = appendBytes(b, 3, pos)
	}
	b = appendFixed32(b, 4, math.Float32bits(6.5))
	b = appendFixed32(b, 5, lastHeard)
	return appendUint(b, 9, hops)
}

func meshPacket(from, to uint32, port int, payload []byte, rx uint32) []byte {
	data := appendUint(nil, 1, uint64(port))
	data = appendBytes(data, 2, payload)
	b := appendFixed32(nil, 1, from)
	b = appendFixed32(b, 2, to)
	b = appendBytes(b, 4, data)
	b = appendFixed32(b, 7, rx)
	b = appendUint(b, 9, 1)
	return appendUint(b, 15, 3)
}

func TestFrameReaderSkipsNoise(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("DEBUG | boot \x94 log text\n")
	buf.Write(frame([]byte("one")))
	buf.Write([]byte{0x94, 0x94, 0xC3, 0, 3})
	buf.WriteString("two")
	buf.Write([]byte{0x94, 0xC3, 0xff, 0xff}) // impossible length: resync
	buf.Write(frame([]byte("three")))
	r := bufio.NewReader(&buf)
	for _, want := range []string{"one", "two", "three"} {
		got, err := readFrame(r)
		if err != nil || string(got) != want {
			t.Fatalf("frame %q: %q %v", want, got, err)
		}
	}
	if _, err := readFrame(r); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
}

func TestDecodePositionRejectsZero(t *testing.T) {
	if _, ok := decodePosition(position(0, 0, 5)); ok {
		t.Fatal("0,0 accepted as a position")
	}
	p, ok := decodePosition(position(38.8895, -77.0353, 12))
	if !ok || math.Abs(p.Lat-38.8895) > 1e-6 || math.Abs(p.Lon+77.0353) > 1e-6 || p.Alt != 12 {
		t.Fatalf("position: %+v %v", p, ok)
	}
}

func TestManagerReadsRadio(t *testing.T) {
	radio, arcana := net.Pipe()
	defer radio.Close()
	m := New(nil)
	m.dial = func(ctx context.Context, s Settings) (io.ReadWriteCloser, error) { return arcana, nil }
	gotConfig := make(chan []byte, 1)
	go func() {
		r := bufio.NewReader(radio)
		req, err := readFrame(r)
		if err != nil {
			return
		}
		gotConfig <- req
		send := func(field int, b []byte) { radio.Write(frame(appendBytes(nil, field, b))) }
		radio.Write([]byte("INFO | radio log line\r\n"))
		send(3, appendUint(nil, 1, 0x1111)) // my_info
		send(4, encNodeInfo(0x1111, user("!00001111", "Base station KK4ODA", "BASE"), position(37.5, -77.4, 60), 1700000000, 0))
		send(4, encNodeInfo(0x2222, user("!00002222", "Hilltop relay", "HILL"), nil, 1700000100, 2))
		radio.Write(frame(appendUint(nil, 7, 42))) // config_complete_id
		send(2, meshPacket(0x2222, broadcast, portPosition, position(37.6, -77.3, 300), 1700000200))
		send(2, meshPacket(0x3333, broadcast, portText, []byte("Net check-in at 1900 local"), 1700000300))
		send(2, meshPacket(0x3333, 0x1111, portNodeInfo, user("!00003333", "Mobile 1", "MOB1"), 1700000301))
	}()
	m.Apply(context.Background(), Settings{Source: "tcp", Address: "radio.local"})
	defer m.Stop()
	select {
	case req := <-gotConfig:
		var id uint64
		_ = fields(req, func(f field) {
			if f.num == 3 {
				id = f.u
			}
		})
		if id == 0 {
			t.Fatalf("no want_config_id in %x", req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("radio never asked for its configuration")
	}
	var st Status
	deadline := time.Now().Add(5 * time.Second)
	for {
		st = m.Status()
		if len(st.Messages) == 1 && len(st.Nodes) == 3 && st.Nodes[2].ShortName != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.State != StateConnected || st.MyNode != 0x1111 || !st.Nodes[0].Local || st.Nodes[0].ShortName != "BASE" || !st.Nodes[0].Licensed {
		t.Fatalf("local node: %+v", st)
	}
	var hill, mob *Node
	for i := range st.Nodes {
		switch st.Nodes[i].Num {
		case 0x2222:
			hill = &st.Nodes[i]
		case 0x3333:
			mob = &st.Nodes[i]
		}
	}
	if hill == nil || hill.Position == nil || math.Abs(hill.Position.Lat-37.6) > 1e-6 || hill.Position.Alt != 300 || hill.Hops == nil || *hill.Hops != 2 {
		t.Fatalf("relay: %+v", hill)
	}
	if mob == nil || mob.LongName != "Mobile 1" || mob.Position != nil {
		t.Fatalf("mobile: %+v", mob)
	}
	if m := st.Messages[0]; m.Text != "Net check-in at 1900 local" || m.From != 0x3333 || m.Direct {
		t.Fatalf("message: %+v", m)
	}
	m.Stop()
	if m.Status().State != StateOff {
		t.Fatal("not stopped")
	}
}
