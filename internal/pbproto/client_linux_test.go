package pbproto

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// okReply, errReply and lineReply build daemon replies without the request id;
// the harness copies it in, exactly like daemon.c does.
func okReply(args ...string) []string { return append([]string{"r", "", "ok"}, args...) }
func errReply(reason string) []string  { return []string{"r", "", "err", reason} }
func lineReply(text string) []string   { return []string{"r", "", "line", text} }

// testEngine runs a fake control socket and records every request it receives.
type testEngine struct {
	t    *testing.T
	path string
	mu   sync.Mutex
	reqs [][]string
	conn net.Conn
}

func newTestEngine(t *testing.T, handle func(req []string) [][]string) *testEngine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ctl.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	e := &testEngine{t: t, path: path}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go e.serve(conn, handle)
		}
	}()
	return e
}

func (e *testEngine) serve(conn net.Conn, handle func([]string) [][]string) {
	defer conn.Close()
	e.mu.Lock()
	e.conn = conn
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.conn == conn {
			e.conn = nil
		}
		e.mu.Unlock()
	}()
	for {
		req, err := readTestFrame(conn)
		if err != nil {
			return
		}
		if len(req) < 3 || req[0] != "q" {
			return
		}
		e.mu.Lock()
		e.reqs = append(e.reqs, req)
		e.mu.Unlock()

		var frames [][]string
		if handle != nil {
			frames = handle(req)
		}
		if frames == nil {
			switch req[2] {
			case "hello":
				frames = [][]string{okReply("4.0.0", "1", "1")}
			case "log.history":
				frames = [][]string{okReply()}
			default:
				frames = [][]string{okReply("1")}
			}
		}
		for _, f := range frames {
			if len(f) > 1 && f[0] == "r" && f[1] == "" { // empty id = attribute to this request
				f[1] = req[1]
			}
			if err := writeTestFrame(conn, f); err != nil {
				return
			}
		}
	}
}

// send pushes an unsolicited frame (an event, or an id-less refusal).
func (e *testEngine) send(fields ...string) {
	e.mu.Lock()
	conn := e.conn
	e.mu.Unlock()
	if conn == nil {
		e.t.Fatalf("no connection to send on")
	}
	if err := writeTestFrame(conn, fields); err != nil {
		e.t.Fatalf("send: %v", err)
	}
}

func (e *testEngine) requests() [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]string, len(e.reqs))
	copy(out, e.reqs)
	return out
}

// find returns the arguments of the first request with the given command.
func (e *testEngine) find(cmd string) ([]string, bool) {
	for _, r := range e.requests() {
		if len(r) > 2 && r[2] == cmd {
			return r[3:], true
		}
	}
	return nil, false
}

func readTestFrame(r io.Reader) ([]string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n < 2 || n > maxFrame {
		return nil, errors.New("bad frame")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	count := int(binary.BigEndian.Uint16(buf[:2]))
	out := make([]string, 0, count)
	pos := 2
	for i := 0; i < count; i++ {
		if pos+4 > len(buf) {
			return nil, errors.New("short frame")
		}
		l := int(binary.BigEndian.Uint32(buf[pos:]))
		pos += 4
		if l < 0 || pos+l > len(buf) {
			return nil, errors.New("short field")
		}
		out = append(out, string(buf[pos:pos+l]))
		pos += l
	}
	return out, nil
}

func writeTestFrame(w io.Writer, fields []string) error {
	size := 2
	for _, f := range fields {
		size += 4 + len(f)
	}
	buf := make([]byte, 4, 4+size)
	binary.BigEndian.PutUint32(buf, uint32(size))
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(fields)))
	for _, f := range fields {
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(f)))
		buf = append(buf, f...)
	}
	_, err := w.Write(buf)
	return err
}

func TestHelloAgreesOnProtocol(t *testing.T) {
	e := newTestEngine(t, nil)
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if c.Version() != linuxIPCProtocol {
		t.Fatalf("version = %d, want %d", c.Version(), linuxIPCProtocol)
	}
	if c.Engine() != "4.0.0" {
		t.Fatalf("engine = %q, want 4.0.0", c.Engine())
	}
	args, ok := e.find("hello")
	if !ok || len(args) != 1 || args[0] != "1" {
		t.Fatalf("hello args = %v, want [1]", args)
	}
}

func TestAddProxyAppliesSendDomain(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "proxy.add" {
			return [][]string{okReply("7")}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	id, err := c.AddProxy(ProxyConfig{Type: ProxySOCKS5, IP: "127.0.0.1", Port: 1080, User: "u", Pass: "p", SendDomain: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
	args, _ := e.find("proxy.add")
	want := []string{"127.0.0.1:1080", strconv.Itoa(int(ProxySOCKS5)), "127.0.0.1", "1080", "u", "p"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("proxy.add args = %v, want %v", args, want)
	}
	sd, ok := e.find("proxy.sendDomain")
	if !ok || strings.Join(sd, " ") != "7 1" {
		t.Fatalf("sendDomain args = %v, want [7 1]", sd)
	}
}

func TestAddRuleMergesTargetsAndSetsLocalhost(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "rule.add" {
			return [][]string{okReply("3")}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	str := func(v string) *string { return &v }
	id, err := c.AddRule(RuleSpec{
		Process:   str("chrome"),
		Hosts:     str("10.0.0.1,192.168.0.0/16"),
		Domains:   str("*.github.com"),
		Ports:     str("443"),
		Protocol:  ProtoTCP,
		Action:    ActionProxy,
		ProxyID:   7,
		Localhost: true,
	})
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if id != 3 {
		t.Fatalf("id = %d, want 3", id)
	}
	args, _ := e.find("rule.add")
	want := []string{"chrome", "10.0.0.1,192.168.0.0/16,*.github.com", "443", "0", "0", "7"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("rule.add args = %v, want %v", args, want)
	}
	lh, ok := e.find("rule.localhost")
	if !ok || strings.Join(lh, " ") != "3 1" {
		t.Fatalf("localhost args = %v, want [3 1]", lh)
	}
}

func TestAddRuleDefaultsToWildcards(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "rule.add" {
			return [][]string{okReply("1")}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if _, err := c.AddRule(RuleSpec{Action: ActionDirect}); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	args, _ := e.find("rule.add")
	want := []string{"*", "*", "*", "0", "1", "0"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("rule.add args = %v, want %v", args, want)
	}
}

func TestFlowsParsesDaemonColumns(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "flows.get" {
			return [][]string{okReply(
				"1\t4242\t7\t1500\t2048\t4096\t443\t0\t1\t" + strings.Repeat("0", 32) + "\tcurl\texample.com",
				"2\t4243\t7\t900\t0\t0\t53\t1\t0\t" + strings.Repeat("0", 32) + "\tdig\t1.1.1.1",
			)}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	flows, err := c.Flows(0)
	if err != nil {
		t.Fatalf("flows: %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("got %d flows, want 2", len(flows))
	}
	f := flows[0]
	if f.ID != 1 || f.PID != 4242 || f.ProxyID != 7 || f.Port != 443 || f.UDP || !f.IPv6 {
		t.Fatalf("flow = %+v", f)
	}
	if f.Sent != 2048 || f.Received != 4096 || f.AgeMS != 1500 {
		t.Fatalf("counters = %+v", f)
	}
	if f.Name != "curl" || f.Host != "example.com" {
		t.Fatalf("names = %q %q", f.Name, f.Host)
	}
	if !flows[1].UDP || flows[1].Port != 53 {
		t.Fatalf("second flow = %+v", flows[1])
	}
}

func TestProxyTestStreamsLines(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "proxy.test" {
			return [][]string{
				lineReply("Proxy: 127.0.0.1:1080 (SOCKS5)"),
				lineReply("[1/5] Connecting to the proxy ..."),
				okReply("0"),
			}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.TestProxy(7, "example.com", 443); err != nil {
		t.Fatalf("test: %v", err)
	}
	var lines int
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			switch ev.Kind {
			case EvtTestLine:
				lines++
			case EvtTestDone:
				if ev.TestResult != 0 {
					t.Fatalf("result = %d, want 0", ev.TestResult)
				}
				if lines != 2 {
					t.Fatalf("got %d lines, want 2", lines)
				}
				return
			}
		case <-deadline:
			t.Fatal("no test result")
		}
	}
}

func TestProxyTestReportsEngineFailure(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "proxy.test" {
			return [][]string{
				lineReply("      FAILED: connect to 127.0.0.1:1080 failed: Connection refused"),
				okReply("-1"),
			}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.TestProxy(7, "example.com", 443); err != nil {
		t.Fatalf("test: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind == EvtTestDone {
				if ev.TestResult != -1 {
					t.Fatalf("result = %d, want -1", ev.TestResult)
				}
				return
			}
		case <-deadline:
			t.Fatal("no test result")
		}
	}
}

func TestUnattributedRefusalIsDenied(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "hello" {
			return [][]string{{"r", "0", "err", "not allowed: only root and members of the group 'proxybridge' may control ProxyBridge"}}
		}
		return nil
	})
	_, err := DialSocket(e.path)
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("dial error = %v, want ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "proxybridge") {
		t.Fatalf("error %q should name the group", err)
	}
}

func TestConnEventIsDecoded(t *testing.T) {
	var e *testEngine
	e = newTestEngine(t, func(req []string) [][]string {
		if req[2] == "hello" {
			go func() {
				time.Sleep(30 * time.Millisecond)
				e.send("e", "conn", "curl", "4242", "93.184.216.34", "443", "TCP", "SOCKS5 127.0.0.1:1080")
			}()
			return [][]string{okReply("4.0.0", "1", "1")}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind != EvtConn {
				continue
			}
			got := ev.Conn
			if got.Process != "curl" || got.PID != 4242 || got.IP != "93.184.216.34" || got.Port != 443 {
				t.Fatalf("conn = %+v", got)
			}
			if got.Info != "SOCKS5 127.0.0.1:1080" {
				t.Fatalf("info = %q", got.Info)
			}
			return
		case <-deadline:
			t.Fatal("no conn event")
		}
	}
}

func TestRejectionKeepsEngineReason(t *testing.T) {
	e := newTestEngine(t, func(req []string) [][]string {
		if req[2] == "rule.add" {
			return [][]string{errReply("the target filter is not valid")}
		}
		return nil
	})
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	_, err = c.AddRule(RuleSpec{Process: func() *string { s := "curl"; return &s }()})
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("error = %v, want ErrRejected", err)
	}
	if !strings.Contains(err.Error(), "target filter") {
		t.Fatalf("error %q lost the engine's reason", err)
	}
}

func TestDialWithoutSocketIsNoService(t *testing.T) {
	_, err := DialSocket(filepath.Join(t.TempDir(), "missing.sock"))
	if !errors.Is(err, ErrNoService) {
		t.Fatalf("error = %v, want ErrNoService", err)
	}
}

func TestRulePositionIsExplicitlyUnsupported(t *testing.T) {
	e := newTestEngine(t, nil)
	c, err := DialSocket(e.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.RulePosition(1); !errors.Is(err, ErrRejected) {
		t.Fatalf("error = %v, want ErrRejected", err)
	}
}
