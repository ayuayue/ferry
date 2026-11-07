// Package pbproto speaks the wire protocol of the ProxyBridge engine service.
//
// The service (ProxyBridgeSvc.exe, LocalSystem) owns ProxyBridgeCore.dll and
// WinDivert and exposes two message-mode named pipes: one request/response
// pipe and one event pipe. This package is the unprivileged client side, and
// also contains a mock service so the app can run without the real service.
//
// Message layout (little-endian):
//
//	request  : u32 op, args...
//	response : u32 ret, extra...        (except OpGetFlows, which has no ret)
//	event    : u32 type, fields...
//	string   : u32 length + bytes, no NUL; length 0xFFFFFFFF means NULL
package pbproto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// ProtoVersion is the protocol revision of ProxyBridge 4.3.0.
	ProtoVersion = 6

	PipeCtl = `\\.\pipe\ProxyBridgeCtl`
	PipeEvt = `\\.\pipe\ProxyBridgeEvt`

	msgMax = 64 * 1024
	strMax = 32 * 1024

	nullStr = 0xFFFFFFFF
)

// Op is a request opcode.
type Op uint32

const (
	OpHello          Op = 1
	OpAddProxy       Op = 2
	OpEditProxy      Op = 3
	OpDeleteProxy    Op = 4
	OpTestProxyStart Op = 5
	OpAddRule        Op = 6
	OpEnableRule     Op = 7
	OpDisableRule    Op = 8
	OpDeleteRule     Op = 9
	OpEditRule       Op = 10
	OpMoveRule       Op = 11
	OpGetRulePos     Op = 12
	OpSetTrafficLog  Op = 13
	OpClearConnLogs  Op = 14
	OpStart          Op = 15
	OpStop           Op = 16
	OpSetEnabled     Op = 17
	OpSetLogAll      Op = 18
	OpGetFlows       Op = 19
)

// EvtKind is an event type pushed by the service.
type EvtKind uint32

const (
	EvtLog      EvtKind = 1 // a log line from the engine
	EvtConn     EvtKind = 2 // a new connection decision
	EvtTestLine EvtKind = 3 // one line of a proxy check
	EvtTestDone EvtKind = 4 // proxy check finished, Result carries the outcome
)

// ProxyType selects the upstream protocol of a proxy configuration.
type ProxyType uint32

const (
	ProxyHTTP   ProxyType = 0
	ProxySOCKS5 ProxyType = 1
)

func (t ProxyType) String() string {
	if t == ProxySOCKS5 {
		return "SOCKS5"
	}
	return "HTTP"
}

// RuleAction is what happens to connections a rule matches.
type RuleAction uint32

const (
	ActionProxy  RuleAction = 0
	ActionDirect RuleAction = 1
	ActionBlock  RuleAction = 2
)

func (a RuleAction) String() string {
	switch a {
	case ActionDirect:
		return "DIRECT"
	case ActionBlock:
		return "BLOCK"
	default:
		return "PROXY"
	}
}

// Protocol is the transport a rule applies to.
type Protocol uint32

const (
	ProtoTCP  Protocol = 0
	ProtoUDP  Protocol = 1
	ProtoBoth Protocol = 2
)

func (p Protocol) String() string {
	switch p {
	case ProtoUDP:
		return "UDP"
	case ProtoBoth:
		return "TCP+UDP"
	default:
		return "TCP"
	}
}

// ProxyConfig is one upstream proxy. The engine assigns ID; a config with ID 0
// is not applied yet.
type ProxyConfig struct {
	ID         uint32    `json:"id"`
	Type       ProxyType `json:"type"`
	IP         string    `json:"ip"`
	Port       uint16    `json:"port"`
	User       string    `json:"user,omitempty"`
	Pass       string    `json:"pass,omitempty"`
	SendDomain bool      `json:"sendDomain"`
}

// RuleSpec is the payload of a rule, without the engine's ID.
//
// Process is an image name list ("chrome.exe" or "chrome.exe,code.exe") or
// "*" for any process. Hosts/Ports/Domains are engine syntax: comma or
// semicolon separated, "*" wildcards, port ranges like "8000-9000", domain
// patterns like "*.github.com". Nil means "no restriction". Ports default to
// "*" and Domains default to "" (no restriction) when nil.
type RuleSpec struct {
	Process   *string    `json:"process"`
	Hosts     *string    `json:"hosts"`
	Ports     *string    `json:"ports"`
	Domains   *string    `json:"domains"`
	Protocol  Protocol   `json:"protocol"`
	Action    RuleAction `json:"action"`
	ProxyID   uint32     `json:"proxyId"`
	Localhost bool       `json:"localhost"`
}

// Flow is one live connection the engine is relaying through a proxy.
type Flow struct {
	ID       uint64 `json:"id"`
	PID      uint32 `json:"pid"`
	ProxyID  uint32 `json:"proxyId"`
	RuleID   uint32 `json:"ruleId"`
	AgeMS    uint32 `json:"ageMs"` // 0xFFFFFFFF means unknown (UDP)
	Sent     uint64 `json:"sent"`
	Received uint64 `json:"received"`
	Port     uint16 `json:"port"`
	UDP      bool   `json:"udp"`
	IPv6     bool   `json:"ipv6"`
	Name     string `json:"name"` // process image name, truncated to 39 bytes
	Host     string `json:"host"` // domain when the engine resolved one, else the address
}

// ConnEvent is one connection decision (proxy, direct or block).
type ConnEvent struct {
	Process string `json:"process"`
	PID     uint32 `json:"pid"`
	IP      string `json:"ip"`
	Port    uint16 `json:"port"`
	Info    string `json:"info"` // human-readable decision, e.g. "SOCKS5 1.2.3.4:1080"
}

// Event is one message from the event pipe.
type Event struct {
	Kind       EvtKind
	Log        string
	Conn       ConnEvent
	TestLine   string
	TestResult int32
}

// MaxFlows is the engine's per-call ceiling.
const MaxFlows = 400

// ---- writer ----

type enc struct {
	b   []byte
	err error
}

func (e *enc) u32(v uint32) {
	if e.err != nil {
		return
	}
	e.b = binary.LittleEndian.AppendUint32(e.b, v)
}

func (e *enc) str(s *string) {
	if s == nil {
		e.u32(nullStr)
		return
	}
	if len(*s) > strMax {
		e.err = fmt.Errorf("pbproto: string too long (%d bytes)", len(*s))
		return
	}
	e.u32(uint32(len(*s)))
	e.b = append(e.b, *s...)
}

func (e *enc) strVal(s string) { e.str(&s) }

// ---- reader ----

type dec struct {
	b   []byte
	pos int
	err error
}

func (d *dec) u32() uint32 {
	if d.err != nil {
		return 0
	}
	if d.pos+4 > len(d.b) {
		d.err = errors.New("pbproto: short message")
		return 0
	}
	v := binary.LittleEndian.Uint32(d.b[d.pos:])
	d.pos += 4
	return v
}

func (d *dec) u64() uint64 {
	lo := uint64(d.u32())
	hi := uint64(d.u32())
	return lo | hi<<32
}

func (d *dec) str() (string, bool) {
	n := d.u32()
	if d.err != nil {
		return "", false
	}
	if n == nullStr {
		return "", true
	}
	if n > strMax || d.pos+int(n) > len(d.b) {
		d.err = errors.New("pbproto: bad string length")
		return "", false
	}
	s := string(d.b[d.pos : d.pos+int(n)])
	d.pos += int(n)
	return s, false
}

func (d *dec) strVal() string {
	s, _ := d.str()
	return s
}

// errNeedMore means the buffer holds only part of a message; the caller reads
// more and tries again. The event pipe is read in byte mode (the service
// creates it write-only, so the client cannot switch it to message mode), and
// therefore messages may be split or coalesced.
var errNeedMore = errors.New("pbproto: need more data")

// parseEventAt decodes one event at the start of b and reports how many bytes
// it used. It returns errNeedMore when b is a prefix of a valid event.
func parseEventAt(b []byte) (Event, int, error) {
	d := &streamDec{b: b}
	kind, err := d.u32()
	if err != nil {
		return Event{}, 0, err
	}
	ev := Event{Kind: EvtKind(kind)}
	switch ev.Kind {
	case EvtLog:
		if ev.Log, _, err = d.str(); err != nil {
			return Event{}, 0, err
		}
	case EvtConn:
		if ev.Conn.Process, _, err = d.str(); err != nil {
			return Event{}, 0, err
		}
		if ev.Conn.PID, err = d.u32(); err != nil {
			return Event{}, 0, err
		}
		if ev.Conn.IP, _, err = d.str(); err != nil {
			return Event{}, 0, err
		}
		port, err := d.u32()
		if err != nil {
			return Event{}, 0, err
		}
		ev.Conn.Port = uint16(port)
		if ev.Conn.Info, _, err = d.str(); err != nil {
			return Event{}, 0, err
		}
	case EvtTestLine:
		if ev.TestLine, _, err = d.str(); err != nil {
			return Event{}, 0, err
		}
	case EvtTestDone:
		res, err := d.u32()
		if err != nil {
			return Event{}, 0, err
		}
		ev.TestResult = int32(res)
	default:
		return Event{}, 0, fmt.Errorf("pbproto: unknown event %d", kind)
	}
	return ev, d.pos, nil
}

// streamDec reads from a byte stream, asking for more data instead of failing
// when the buffer ends early.
type streamDec struct {
	b   []byte
	pos int
}

func (d *streamDec) u32() (uint32, error) {
	if d.pos+4 > len(d.b) {
		return 0, errNeedMore
	}
	v := binary.LittleEndian.Uint32(d.b[d.pos:])
	d.pos += 4
	return v, nil
}

func (d *streamDec) str() (string, bool, error) {
	n, err := d.u32()
	if err != nil {
		return "", false, err
	}
	if n == nullStr {
		return "", true, nil
	}
	if n > strMax {
		return "", false, errors.New("pbproto: bad string length")
	}
	if d.pos+int(n) > len(d.b) {
		return "", false, errNeedMore
	}
	s := string(d.b[d.pos : d.pos+int(n)])
	d.pos += int(n)
	return s, false, nil
}

// parseFlows decodes the body of an OpGetFlows response.
func parseFlows(b []byte) ([]Flow, error) {
	d := &dec{b: b}
	n := d.u32()
	if n > MaxFlows {
		return nil, fmt.Errorf("pbproto: %d flows in response", n)
	}
	out := make([]Flow, 0, n)
	for i := uint32(0); i < n; i++ {
		var f Flow
		f.ID = d.u64()
		f.PID = d.u32()
		flags := d.u32()
		f.Port = uint16(d.u32())
		f.ProxyID = d.u32()
		f.RuleID = d.u32()
		f.AgeMS = d.u32()
		f.Sent = d.u64()
		f.Received = d.u64()
		f.Name = d.strVal()
		f.Host = d.strVal()
		f.UDP = flags&1 != 0
		f.IPv6 = flags&2 != 0
		if d.err != nil {
			return nil, d.err
		}
		out = append(out, f)
	}
	return out, nil
}
