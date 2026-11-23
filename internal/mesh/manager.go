package mesh

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.bug.st/serial"
)

// Settings select the radio.
type Settings struct {
	// Source is "" (off), "tcp" or "serial".
	Source string
	// Address is host[:port] of a radio on the network (port 4403).
	Address    string
	SerialPort string
	BaudRate   int
}

// Node is one radio on the mesh.
type Node struct {
	Num       uint32    `json:"num"`
	ID        string    `json:"id"`
	LongName  string    `json:"longName"`
	ShortName string    `json:"shortName"`
	Licensed  bool      `json:"licensed,omitempty"`
	Position  *Position `json:"position,omitempty"`
	LastHeard int64     `json:"lastHeard,omitempty"`
	SNR       float32   `json:"snr,omitempty"`
	Hops      *int      `json:"hops,omitempty"`
	// Local is the radio Arcana is connected to.
	Local bool `json:"local,omitempty"`
}

// Message is a text message heard on a channel or sent to the local node.
type Message struct {
	Time    int64  `json:"time"`
	From    uint32 `json:"from"`
	To      uint32 `json:"to"`
	Channel uint32 `json:"channel"`
	Text    string `json:"text"`
	Direct  bool   `json:"direct"`
}

// States.
const (
	StateOff        = "off"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateError      = "error"
)

// Status is the manager's snapshot.
type Status struct {
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"`
	Source   string    `json:"source"`
	MyNode   uint32    `json:"myNode,omitempty"`
	Nodes    []Node    `json:"nodes"`
	Messages []Message `json:"messages"`
	// Since is when the state last changed.
	Since time.Time `json:"since"`
}

// Manager keeps a connection to one radio and the mesh's state.
type Manager struct {
	log *slog.Logger
	// dial replaces the connection in tests.
	dial func(ctx context.Context, s Settings) (io.ReadWriteCloser, error)
	now  func() time.Time

	mu       sync.Mutex
	settings Settings
	cancel   context.CancelFunc
	done     chan struct{}
	state    string
	err      string
	since    time.Time
	myNode   uint32
	nodes    map[uint32]*Node
	messages []Message
}

// New creates a stopped manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log, state: StateOff, since: time.Now(), nodes: map[uint32]*Node{}, now: time.Now}
}

// maxMessages bounds the message history kept in memory.
const maxMessages = 200

// Apply connects to the radio in s (or disconnects when s.Source is "").
func (m *Manager) Apply(ctx context.Context, s Settings) {
	m.mu.Lock()
	if m.cancel != nil && m.settings == s {
		m.mu.Unlock()
		return
	}
	m.stopLocked()
	m.settings = s
	m.nodes = map[uint32]*Node{}
	m.messages = nil
	m.myNode = 0
	if s.Source == "" {
		m.setState(StateOff, "")
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	done := make(chan struct{})
	m.done = done
	m.setState(StateConnecting, "")
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.loop(ctx, s)
	}()
}

// Stop disconnects.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopLocked()
	m.setState(StateOff, "")
	m.mu.Unlock()
}

func (m *Manager) stopLocked() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	done := m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	<-done
	m.mu.Lock()
}

func (m *Manager) setState(state, err string) {
	if m.state != state || m.err != err {
		m.since = time.Now()
	}
	m.state, m.err = state, err
}

func (m *Manager) loop(ctx context.Context, s Settings) {
	backoff := 2 * time.Second
	for {
		err := m.session(ctx, s)
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		m.setState(StateError, describe(s, err))
		m.mu.Unlock()
		m.log.Info("meshtastic radio", "err", err, "retry", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func describe(s Settings, err error) string {
	where := s.Address
	if s.Source == "serial" {
		where = s.SerialPort
	}
	return fmt.Sprintf("%s: %v. Arcana keeps trying.", where, err)
}

func (m *Manager) open(ctx context.Context, s Settings) (io.ReadWriteCloser, error) {
	if m.dial != nil {
		return m.dial(ctx, s)
	}
	switch s.Source {
	case "tcp":
		addr := s.Address
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(strings.Trim(addr, "[]"), "4403")
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, "tcp", addr)
	case "serial":
		baud := s.BaudRate
		if baud <= 0 {
			baud = 115200
		}
		return serial.Open(s.SerialPort, &serial.Mode{BaudRate: baud})
	}
	return nil, fmt.Errorf("unknown radio connection %q", s.Source)
}

func random32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:]) | 1
}

// session runs one connection until it fails.
func (m *Manager) session(ctx context.Context, s Settings) error {
	conn, err := m.open(ctx, s)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var wmu sync.Mutex
	write := func(p []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_, err := conn.Write(frame(p))
		return err
	}
	if err := write(wantConfig(random32())); err != nil {
		return err
	}
	hb := time.NewTicker(time.Minute)
	defer hb.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.C:
				if write(heartbeat(random32())) != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	r := bufio.NewReaderSize(conn, 4096)
	first := true
	for {
		p, err := readFrame(r)
		if err != nil {
			return err
		}
		if first {
			first = false
			m.mu.Lock()
			m.setState(StateConnected, "")
			m.mu.Unlock()
			m.log.Info("meshtastic radio connected", "source", s.Source)
		}
		m.handle(p)
	}
}

// handle applies one FromRadio message.
func (m *Manager) handle(b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = fields(b, func(f field) {
		switch f.num {
		case 2:
			if p, ok := decodePacket(f.b); ok {
				m.packetLocked(p)
			}
		case 3:
			_ = fields(f.b, func(g field) {
				if g.num == 1 {
					m.myNode = uint32(g.u)
				}
			})
		case 4:
			if n, ok := decodeNodeInfo(f.b); ok {
				node := m.nodeLocked(n.num)
				if n.user != nil {
					node.ID, node.LongName, node.ShortName, node.Licensed = n.user.ID, n.user.LongName, n.user.ShortName, n.user.IsLicensed
				}
				if n.pos != nil {
					node.Position = n.pos
				}
				if n.lastHeard > node.LastHeard {
					node.LastHeard = n.lastHeard
				}
				if n.snr != 0 {
					node.SNR = n.snr
				}
				if n.hasHops {
					h := n.hops
					node.Hops = &h
				}
			}
		}
	})
}

func (m *Manager) nodeLocked(num uint32) *Node {
	n := m.nodes[num]
	if n == nil {
		n = &Node{Num: num, ID: fmt.Sprintf("!%08x", num)}
		m.nodes[num] = n
	}
	return n
}

const broadcast = 0xffffffff

func (m *Manager) packetLocked(p packet) {
	if !p.decoded || p.from == 0 {
		return
	}
	node := m.nodeLocked(p.from)
	heard := p.rxTime
	if heard == 0 {
		heard = m.now().Unix()
	}
	if heard > node.LastHeard {
		node.LastHeard = heard
	}
	if p.snr != 0 {
		node.SNR = p.snr
	}
	if p.hopStart > 0 && p.hopStart >= p.hopLimit {
		h := p.hopStart - p.hopLimit
		node.Hops = &h
	}
	switch p.port {
	case portPosition:
		if pos, ok := decodePosition(p.payload); ok {
			if pos.Time == 0 {
				pos.Time = heard
			}
			node.Position = &pos
		}
	case portNodeInfo:
		if u, ok := decodeUser(p.payload); ok {
			node.ID, node.LongName, node.ShortName, node.Licensed = u.ID, u.LongName, u.ShortName, u.IsLicensed
		}
	case portText:
		if !utf8.Valid(p.payload) || len(p.payload) == 0 {
			return
		}
		m.messages = append(m.messages, Message{Time: heard, From: p.from, To: p.to, Channel: p.channel,
			Text: clip(string(p.payload), 300), Direct: p.to != broadcast})
		if len(m.messages) > maxMessages {
			m.messages = m.messages[len(m.messages)-maxMessages:]
		}
	}
}

// Status returns a snapshot: nodes most recently heard first.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: m.state, Error: m.err, Source: m.settings.Source, MyNode: m.myNode, Since: m.since,
		Nodes: []Node{}, Messages: append([]Message{}, m.messages...)}
	for _, n := range m.nodes {
		c := *n
		c.Local = n.Num == m.myNode
		if n.Position != nil {
			p := *n.Position
			c.Position = &p
		}
		st.Nodes = append(st.Nodes, c)
	}
	sort.Slice(st.Nodes, func(i, j int) bool {
		if st.Nodes[i].Local != st.Nodes[j].Local {
			return st.Nodes[i].Local
		}
		return st.Nodes[i].LastHeard > st.Nodes[j].LastHeard
	})
	return st
}
