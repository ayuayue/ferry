package pbproto

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// Mock is a stand-in for ProxyBridgeSvc.exe that speaks the same protocol
// with synthetic traffic. It needs no administrator rights and no driver, so
// the app can be developed and demoed without installing the service.
type Mock struct {
	CtlName string
	EvtName string

	mu      sync.Mutex
	ctl     windows.Handle
	evt     windows.Handle
	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once

	writeMu sync.Mutex
	rnd     *rand.Rand

	proxies  []ProxyConfig
	rules    []mockRule
	flows    []Flow
	nextID   uint32
	enabled  bool
	logAll   bool
	traffic  bool
	started  bool
	connSeqs int
}

type mockRule struct {
	id      uint32
	spec    RuleSpec
	enabled bool
}

// NewMock returns a mock service with a few proxies, rules and live flows.
func NewMock() *Mock {
	m := &Mock{
		// Per-process names: two PBDeck instances (dev + installed) must not
		// fight over the mock, and mock pipes never touch the real service's.
		CtlName: `\\.\pipe\PBDeckMock-` + strconv.Itoa(os.Getpid()) + `-Ctl`,
		EvtName: `\\.\pipe\PBDeckMock-` + strconv.Itoa(os.Getpid()) + `-Evt`,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
		rnd:     rand.New(rand.NewPCG(0x50, 0x0d)),
		enabled: true,
	}
	// The app's store is the source of truth: the mock starts with no proxies
	// and no rules and gets them replayed like the real service does.
	m.nextID = 1
	m.seedFlows()
	return m
}

func strp(s string) *string { return &s }

func (m *Mock) seedFlows() {
	apps := []struct {
		name, host string
		port       uint16
	}{
		{"chrome.exe", "api.github.com", 443},
		{"chrome.exe", "github.com", 443},
		{"Code.exe", "marketplace.visualstudio.com", 443},
		{"Code.exe", "20.205.243.166", 22},
		{"msedge.exe", "registry.npmjs.org", 443},
		{"WeChat.exe", "long.weixin.qq.com", 443},
		{"telegram.exe", "149.154.167.51", 443},
	}
	for i, a := range apps {
		m.flows = append(m.flows, Flow{
			ID:       uint64(1000 + i),
			PID:      uint32(2000 + i*37),
			ProxyID:  1,
			RuleID:   1,
			AgeMS:    uint32(i * 47000),
			Sent:     uint64(4096 * (i + 1)),
			Received: uint64(16384 * (i + 2)),
			Port:     a.port,
			Name:     a.name,
			Host:     a.host,
		})
	}
}

// Start creates both pipes and begins serving in the background.
func (m *Mock) Start() error {
	var err error
	m.ctl, err = m.makePipe(m.CtlName, windows.PIPE_ACCESS_DUPLEX)
	if err != nil {
		return fmt.Errorf("mock: control pipe: %w", err)
	}
	m.evt, err = m.makePipe(m.EvtName, windows.PIPE_ACCESS_OUTBOUND)
	if err != nil {
		windows.CloseHandle(m.ctl)
		return fmt.Errorf("mock: event pipe: %w", err)
	}
	go m.accept()
	return nil
}

func (m *Mock) makePipe(name string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateNamedPipe(p,
		access|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, msgMax, msgMax, 0, nil)
}

// Close stops the mock and releases the pipes.
func (m *Mock) Close() {
	m.once.Do(func() { close(m.stop) })
	select {
	case <-m.stopped:
	case <-time.After(2 * time.Second):
	}
	windows.CloseHandle(m.ctl)
	windows.CloseHandle(m.evt)
}

func (m *Mock) accept() {
	defer close(m.stopped)
	err := windows.ConnectNamedPipe(m.ctl, nil)
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return
	}
	// The real service gives a client five seconds to open the event pipe.
	evtConnected := make(chan error, 1)
	go func() {
		err := windows.ConnectNamedPipe(m.evt, nil)
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			err = nil
		}
		evtConnected <- err
	}()
	select {
	case err := <-evtConnected:
		if err != nil {
			return
		}
	case <-time.After(5 * time.Second):
		return
	}

	go m.emitLoop()

	buf := make([]byte, msgMax)
	for {
		var n uint32
		if err := windows.ReadFile(m.ctl, buf, &n, nil); err != nil {
			return
		}
		req := buf[:n]
		resp, ok := m.dispatch(req)
		if !ok {
			return
		}
		if err := m.writeCtl(resp); err != nil {
			return
		}
	}
}

func (m *Mock) writeCtl(b []byte) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return windows.WriteFile(m.ctl, b, nil, nil)
}

func (m *Mock) emit(ev Event) {
	b := encodeEvent(ev)
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	_ = windows.WriteFile(m.evt, b, nil, nil)
}

func encodeEvent(ev Event) []byte {
	e := &enc{}
	e.u32(uint32(ev.Kind))
	switch ev.Kind {
	case EvtLog:
		e.strVal(ev.Log)
	case EvtConn:
		e.strVal(ev.Conn.Process)
		e.u32(ev.Conn.PID)
		e.strVal(ev.Conn.IP)
		e.u32(uint32(ev.Conn.Port))
		e.strVal(ev.Conn.Info)
	case EvtTestLine:
		e.strVal(ev.TestLine)
	case EvtTestDone:
		e.u32(uint32(ev.TestResult))
	}
	return e.b
}

// emitLoop fakes traffic: connection decisions, engine log lines and growing
// flows. This is what makes the UI feel alive in mock mode.
func (m *Mock) emitLoop() {
	conns := []struct {
		app, ip, info string
		port          uint16
	}{
		{"chrome.exe", "140.82.121.4", "SOCKS5 127.0.0.1:1080 (rule #1)", 443},
		{"chrome.exe", "185.199.108.133", "SOCKS5 127.0.0.1:1080 (rule #1)", 443},
		{"Code.exe", "20.205.243.166", "SOCKS5 hk.example.net:7891 (rule #2)", 22},
		{"msedge.exe", "104.16.25.34", "SOCKS5 127.0.0.1:1080 (rule #1)", 443},
		{"WeChat.exe", "101.91.37.14", "Direct (no rule matched)", 443},
		{"steam.exe", "23.45.72.9", "Blocked (rule #4)", 27015},
		{"git.exe", "140.82.113.4", "Direct (rule #3)", 22},
		{"telegram.exe", "149.154.167.51", "SOCKS5 hk.example.net:7891 (rule #2)", 443},
		{"curl.exe", "1.1.1.1", "Direct (no rule matched)", 443},
	}
	tickConn := time.NewTicker(900 * time.Millisecond)
	tickFlow := time.NewTicker(1 * time.Second)
	tickLog := time.NewTicker(7 * time.Second)
	defer tickConn.Stop()
	defer tickFlow.Stop()
	defer tickLog.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-tickConn.C:
			c := conns[m.connSeqs%len(conns)]
			m.connSeqs++
			m.emit(Event{Kind: EvtConn, Conn: ConnEvent{
				Process: c.app, PID: uint32(3000 + m.connSeqs%5), IP: c.ip, Port: c.port, Info: c.info,
			}})
		case <-tickFlow.C:
			m.mu.Lock()
			for i := range m.flows {
				m.flows[i].Sent += uint64(m.rnd.IntN(9000))
				m.flows[i].Received += uint64(m.rnd.IntN(40000))
				if m.flows[i].AgeMS != 0xFFFFFFFF {
					m.flows[i].AgeMS += 1000
				}
			}
			if m.rnd.IntN(4) == 0 {
				i := m.rnd.IntN(len(m.flows))
				f := m.flows[i]
				f.ID = uint64(2000 + int(m.nextID))
				m.nextID++
				f.AgeMS = 0
				f.Sent = 0
				f.Received = 0
				m.flows[i] = f
			}
			m.mu.Unlock()
		case <-tickLog.C:
			m.emit(Event{Kind: EvtLog, Log: "engine: 8 active flows, 3 rules matched in the last second"})
		}
	}
}

func (m *Mock) dispatch(req []byte) ([]byte, bool) {
	d := &dec{b: req}
	op := Op(d.u32())
	out := &enc{}

	switch op {
	case OpHello:
		out.u32(ProtoVersion)
	case OpAddProxy:
		p := ProxyConfig{Type: ProxyType(d.u32())}
		p.IP = d.strVal()
		p.Port = uint16(d.u32())
		user, nu := d.str()
		pass, np := d.str()
		p.SendDomain = d.u32() != 0
		if !nu {
			p.User = user
		}
		if !np {
			p.Pass = pass
		}
		if d.err != nil {
			return nil, false
		}
		m.mu.Lock()
		p.ID = m.nextID
		m.nextID++
		m.proxies = append(m.proxies, p)
		m.mu.Unlock()
		out.u32(p.ID)
	case OpEditProxy:
		id := d.u32()
		p := ProxyConfig{ID: id, Type: ProxyType(d.u32())}
		p.IP = d.strVal()
		p.Port = uint16(d.u32())
		user, nu := d.str()
		pass, np := d.str()
		p.SendDomain = d.u32() != 0
		if !nu {
			p.User = user
		}
		if !np {
			p.Pass = pass
		}
		ok := false
		m.mu.Lock()
		for i := range m.proxies {
			if m.proxies[i].ID == id {
				m.proxies[i] = p
				ok = true
			}
		}
		m.mu.Unlock()
		out.u32(boolU32(ok))
	case OpDeleteProxy:
		id := d.u32()
		ok := false
		m.mu.Lock()
		for i := range m.proxies {
			if m.proxies[i].ID == id {
				m.proxies = append(m.proxies[:i], m.proxies[i+1:]...)
				ok = true
				break
			}
		}
		m.mu.Unlock()
		out.u32(boolU32(ok))
	case OpTestProxyStart:
		id := d.u32()
		host := d.strVal()
		port := d.u32()
		go m.runTest(id, host, uint16(port))
		out.u32(1)
	case OpAddRule, OpEditRule:
		var id uint32
		if op == OpEditRule {
			id = d.u32()
		}
		var s RuleSpec
		if v, isNull := d.str(); !isNull {
			s.Process = &v
		}
		if v, isNull := d.str(); !isNull {
			s.Hosts = &v
		}
		if v, isNull := d.str(); !isNull {
			s.Ports = &v
		}
		if v, isNull := d.str(); !isNull {
			s.Domains = &v
		}
		s.Protocol = Protocol(d.u32())
		s.Action = RuleAction(d.u32())
		s.ProxyID = d.u32()
		s.Localhost = d.u32() != 0
		if d.err != nil {
			return nil, false
		}
		m.mu.Lock()
		if op == OpAddRule {
			id = m.nextID
			m.nextID++
			m.rules = append(m.rules, mockRule{id: id, spec: s, enabled: true})
		} else {
			for i := range m.rules {
				if m.rules[i].id == id {
					m.rules[i].spec = s
				}
			}
		}
		m.mu.Unlock()
		out.u32(id)
	case OpEnableRule, OpDisableRule:
		id := d.u32()
		on := op == OpEnableRule
		m.mu.Lock()
		for i := range m.rules {
			if m.rules[i].id == id {
				m.rules[i].enabled = on
			}
		}
		m.mu.Unlock()
		out.u32(1)
	case OpDeleteRule:
		id := d.u32()
		m.mu.Lock()
		for i := range m.rules {
			if m.rules[i].id == id {
				m.rules = append(m.rules[:i], m.rules[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
		out.u32(1)
	case OpMoveRule:
		id, pos := d.u32(), d.u32()
		m.mu.Lock()
		from := -1
		for i := range m.rules {
			if m.rules[i].id == id {
				from = i
			}
		}
		if from >= 0 {
			to := int(pos) - 1
			if to < 0 {
				to = 0
			}
			if to >= len(m.rules) {
				to = len(m.rules) - 1
			}
			r := m.rules[from]
			m.rules = append(m.rules[:from], m.rules[from+1:]...)
			m.rules = append(m.rules[:to], append([]mockRule{r}, m.rules[to:]...)...)
			out.u32(1)
		} else {
			out.u32(0)
		}
		m.mu.Unlock()
	case OpGetRulePos:
		id := d.u32()
		pos := uint32(0)
		m.mu.Lock()
		for i := range m.rules {
			if m.rules[i].id == id {
				pos = uint32(i + 1)
			}
		}
		m.mu.Unlock()
		out.u32(pos)
	case OpSetTrafficLog:
		m.mu.Lock()
		m.traffic = d.u32() != 0
		m.mu.Unlock()
		out.u32(0)
	case OpClearConnLogs:
		out.u32(0)
	case OpStart:
		m.mu.Lock()
		m.started = true
		m.mu.Unlock()
		out.u32(1)
	case OpStop:
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		out.u32(1)
	case OpSetEnabled:
		m.mu.Lock()
		m.enabled = d.u32() != 0
		m.mu.Unlock()
		out.u32(0)
	case OpSetLogAll:
		m.mu.Lock()
		m.logAll = d.u32() != 0
		m.mu.Unlock()
		out.u32(0)
	case OpGetFlows:
		max := d.u32()
		if max > MaxFlows {
			max = MaxFlows
		}
		rows := &enc{}
		m.mu.Lock()
		n := uint32(len(m.flows))
		if n > max {
			n = max
		}
		rows.u32(n)
		for i := uint32(0); i < n; i++ {
			f := m.flows[i]
			rows.u32(uint32(f.ID))
			rows.u32(uint32(f.ID >> 32))
			rows.u32(f.PID)
			var flags uint32
			if f.UDP {
				flags |= 1
			}
			if f.IPv6 {
				flags |= 2
			}
			rows.u32(flags)
			rows.u32(uint32(f.Port))
			rows.u32(f.ProxyID)
			rows.u32(f.RuleID)
			rows.u32(f.AgeMS)
			rows.u32(uint32(f.Sent))
			rows.u32(uint32(f.Sent >> 32))
			rows.u32(uint32(f.Received))
			rows.u32(uint32(f.Received >> 32))
			rows.strVal(f.Name)
			rows.strVal(f.Host)
		}
		m.mu.Unlock()
		if rows.err != nil {
			return nil, false
		}
		return rows.b, true
	default:
		return nil, false
	}
	if d.err != nil {
		return nil, false
	}
	return out.b, true
}

func (m *Mock) runTest(id uint32, host string, port uint16) {
	steps := []struct {
		d    time.Duration
		line string
	}{
		{0, fmt.Sprintf("resolving proxy config #%d", id)},
		{180 * time.Millisecond, "TCP connect to proxy: 12 ms"},
		{320 * time.Millisecond, fmt.Sprintf("tunnel to %s:%d: established", host, port)},
		{280 * time.Millisecond, "TLS handshake: 87 ms, TLS 1.3"},
		{240 * time.Millisecond, "HTTP GET: 200 OK, 1.4 KB in 61 ms"},
		{260 * time.Millisecond, "SOCKS5 UDP ASSOCIATE: supported"},
	}
	for _, s := range steps {
		select {
		case <-m.stop:
			return
		case <-time.After(s.d):
		}
		m.emit(Event{Kind: EvtTestLine, TestLine: s.line})
	}
	m.emit(Event{Kind: EvtTestDone, TestResult: 0})
}
