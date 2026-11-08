//go:build linux

// Linux client for the ProxyBridge daemon (4.x).
//
// Windows talks to a LocalSystem service over two named pipes with a binary
// protocol. Linux has a different transport and a different wire shape, so this
// file implements the same Go API on top of the daemon's Unix control socket.
// Everything above the wire — the operations this package offers, the events it
// emits, the error set — stays identical, so session.go does not need to know
// which of the two transports it is using.
//
// Frame layout, network byte order, mirroring pb_ipc.h:
//
//	u32 length | u16 field count | (u32 field length | bytes)*
//
// The first field is the message kind: "q" request, "r" reply, "e" event.
//
//	request : "q" <id> <command> <arg>...
//	reply   : "r" <id> "ok" <result>...   | "r" <id> "err" <reason>
//	stream  : "r" <id> "line" <text>      (proxy.test progress, before the reply)
//	event   : "e" <name> <arg>...
package pbproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// linuxIPCProtocol is PB_IPC_PROTOCOL from the engine's pb_ipc.h.
	linuxIPCProtocol = 1

	// DefaultSocket is where the engine's systemd unit puts its control socket.
	DefaultSocket = "/run/proxybridge/ctl.sock"

	// SocketEnv points Ferry at a different control socket. It exists so a user
	// can drive an engine started by hand, and so tests can use a throwaway path.
	SocketEnv = "FERRY_ENGINE_SOCKET"

	// maxFrame and maxFields mirror PB_IPC_MAX_FRAME / PB_IPC_MAX_FIELDS.
	maxFrame  = 8 << 20
	maxFields = 4096

	// replyBuf holds the streamed lines of a proxy check until the reader picks
	// them up; a check produces well under this many.
	replyBuf = 64

	callTimeout = 8 * time.Second
	testTimeout = 40 * time.Second
)

// reply is one message addressed to a request id.
type reply struct {
	kind   string // "ok", "err" or "line"
	args   []string
	reason string
}

// Client is a connection to the engine daemon.
type Client struct {
	conn net.Conn

	wmu sync.Mutex // serialises frame writes

	mu      sync.Mutex // guards pending, closed and the event channel
	nextID  uint32
	pending map[uint32]chan reply
	closed  bool

	once sync.Once

	events chan Event
	errs   chan error

	// refusals carries replies the daemon could not attribute to a request id.
	// It sends `r 0 err "not allowed: ..."` before closing when the caller is
	// not root and not in the engine group, so the dialog can say why instead
	// of waiting for a timeout.
	refusals chan reply

	version uint32
	engine  string // daemon version, e.g. "4.0.0"
}

// SocketPath is the control socket this build dials.
func SocketPath() string {
	if p := strings.TrimSpace(os.Getenv(SocketEnv)); p != "" {
		return p
	}
	return DefaultSocket
}

// Dial connects to the installed engine.
func Dial() (*Client, error) { return DialSocket(SocketPath()) }

// DialSocket connects to the control socket at path.
func DialSocket(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, dialError(path, err)
	}
	c := &Client{
		conn:     conn,
		pending:  make(map[uint32]chan reply),
		events:   make(chan Event, 1024),
		errs:     make(chan error, 1),
		refusals: make(chan reply, 8),
	}
	go c.readLoop()

	ver, engine, err := c.hello()
	if err != nil {
		c.Close()
		return nil, err
	}
	c.version, c.engine = ver, engine
	c.replayHistory()
	return c, nil
}

// dialError turns a socket error into the same set the Windows client reports,
// so the UI can offer the right fix on either platform.
func dialError(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: no control socket at %s", ErrNoService, path)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%w: nothing is listening on %s", ErrNoService, path)
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %s (join the engine group: sudo usermod -aG proxybridge $USER, then log in again)", ErrDenied, path)
	case errors.Is(err, syscall.ENOTSOCK), errors.Is(err, syscall.EPROTOTYPE):
		return fmt.Errorf("%w: %s is not a socket", ErrNoService, path)
	}
	return err
}

// connError tags a dead socket so callers can tell it apart from a refusal.
func connError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return fmt.Errorf("%w: %v", ErrDisconnected, err)
	}
	return err
}

// Version is the IPC protocol revision agreed at dial time.
func (c *Client) Version() uint32 { return c.version }

// Engine is the daemon's own version string, e.g. "4.0.0".
func (c *Client) Engine() string { return c.engine }

// Events carries engine events. It is closed when the connection ends.
func (c *Client) Events() <-chan Event { return c.events }

// Errors receives the reason the connection ended, at most once.
func (c *Client) Errors() <-chan error { return c.errs }

func (c *Client) readLoop() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
			c.shutdown(err)
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n < 2 || n > maxFrame {
			c.shutdown(fmt.Errorf("pbproto: frame length %d out of range", n))
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.conn, buf); err != nil {
			c.shutdown(err)
			return
		}
		fields, err := splitFrame(buf)
		if err != nil {
			c.shutdown(err)
			return
		}
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "r":
			c.deliver(fields)
		case "e":
			c.dispatch(fields)
		}
	}
}

// splitFrame decodes the fields of one frame body, which starts with the count.
func splitFrame(buf []byte) ([]string, error) {
	if len(buf) < 2 {
		return nil, errors.New("pbproto: short frame")
	}
	count := int(binary.BigEndian.Uint16(buf[:2]))
	if count > maxFields {
		return nil, fmt.Errorf("pbproto: %d fields in one frame", count)
	}
	out := make([]string, 0, count)
	pos := 2
	for i := 0; i < count; i++ {
		if pos+4 > len(buf) {
			return nil, errors.New("pbproto: truncated field length")
		}
		l := int(binary.BigEndian.Uint32(buf[pos:]))
		pos += 4
		if l < 0 || pos+l > len(buf) {
			return nil, errors.New("pbproto: truncated field")
		}
		out = append(out, string(buf[pos:pos+l]))
		pos += l
	}
	return out, nil
}

func (c *Client) deliver(fields []string) {
	if len(fields) < 3 {
		return
	}
	id, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil {
		return
	}
	r := reply{kind: fields[2]}
	if len(fields) > 3 {
		r.args = fields[3:]
	}
	if r.kind == "err" && len(r.args) > 0 {
		r.reason = r.args[0]
	}
	if id == 0 {
		// Not addressed to any request: the daemon is refusing the session.
		select {
		case c.refusals <- r:
		default:
		}
		return
	}
	c.mu.Lock()
	if ch, ok := c.pending[uint32(id)]; ok {
		select {
		case ch <- r:
		default: // the waiter is gone or a whole check is streaming faster than we read
		}
	}
	c.mu.Unlock()
}

func (c *Client) dispatch(fields []string) {
	if len(fields) < 2 {
		return
	}
	switch fields[1] {
	case "conn":
		// e conn <proc> <pid> <ip> <port> <proto> <via>
		if len(fields) < 8 {
			return
		}
		pid, _ := strconv.ParseUint(fields[3], 10, 32)
		port, _ := strconv.ParseUint(fields[5], 10, 32)
		info := fields[7]
		if info == "" {
			info = fields[6]
		}
		c.emit(Event{Kind: EvtConn, Conn: ConnEvent{
			Process: fields[2],
			PID:     uint32(pid),
			IP:      fields[4],
			Port:    uint16(port),
			Info:    info,
		}})
	case "log":
		if len(fields) < 3 {
			return
		}
		c.emit(Event{Kind: EvtLog, Log: fields[2]})
	}
	// Unknown events are ignored: the daemon may grow new ones and losing a
	// message is better than dropping the whole connection over it.
}

func (c *Client) emit(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.events <- ev:
	default: // the UI is behind: drop, it polls flows anyway
	}
}

func (c *Client) shutdown(cause error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			delete(c.pending, id)
			close(ch)
		}
		close(c.events)
		c.mu.Unlock()

		_ = c.conn.Close()
		if cause == nil || errors.Is(cause, net.ErrClosed) || errors.Is(cause, io.EOF) || errors.Is(cause, io.ErrUnexpectedEOF) {
			cause = ErrClosed
		}
		select {
		case c.errs <- cause:
		default:
		}
	})
}

// Close ends the connection. Safe to call more than once.
func (c *Client) Close() error {
	c.shutdown(ErrClosed)
	return nil
}

func (c *Client) writeFrame(fields []string) error {
	size := 2
	for _, f := range fields {
		size += 4 + len(f)
	}
	if size > maxFrame {
		return fmt.Errorf("pbproto: frame of %d bytes is too large", size)
	}
	buf := make([]byte, 4+size)
	binary.BigEndian.PutUint32(buf, uint32(size))
	binary.BigEndian.PutUint16(buf[4:], uint16(len(fields)))
	pos := 6
	for _, f := range fields {
		binary.BigEndian.PutUint32(buf[pos:], uint32(len(f)))
		pos += 4
		copy(buf[pos:], f)
		pos += len(f)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(buf)
	return err
}

// begin writes a request and returns the channel its replies arrive on.
func (c *Client) begin(cmd string, args ...string) (uint32, chan reply, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, nil, ErrDisconnected
	}
	c.nextID++
	id := c.nextID
	ch := make(chan reply, replyBuf)
	c.pending[id] = ch
	c.mu.Unlock()

	fields := make([]string, 0, len(args)+3)
	fields = append(fields, "q", strconv.FormatUint(uint64(id), 10), cmd)
	fields = append(fields, args...)
	if err := c.writeFrame(fields); err != nil {
		c.forget(id)
		return 0, nil, connError(err)
	}
	return id, ch, nil
}

func (c *Client) forget(id uint32) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// request performs one call and waits for its reply.
func (c *Client) request(cmd string, args ...string) (*reply, error) {
	id, ch, err := c.begin(cmd, args...)
	if err != nil {
		return nil, err
	}
	defer c.forget(id)
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, ErrDisconnected
		}
		return &r, nil
	case r := <-c.refusals:
		return nil, refusalError(r.reason)
	case <-time.After(callTimeout):
		return nil, ErrTimeout
	}
}

func (c *Client) okCall(cmd string, args ...string) error {
	r, err := c.request(cmd, args...)
	if err != nil {
		return err
	}
	if r.kind != "ok" {
		return rejected(r.reason)
	}
	return nil
}

func (c *Client) idCall(cmd string, args ...string) (uint32, error) {
	r, err := c.request(cmd, args...)
	if err != nil {
		return 0, err
	}
	if r.kind != "ok" {
		return 0, rejected(r.reason)
	}
	if len(r.args) == 0 {
		return 0, ErrRejected
	}
	n, err := strconv.ParseUint(r.args[0], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("pbproto: %s answered with %q", cmd, r.args[0])
	}
	if n == 0 {
		return 0, ErrRejected
	}
	return uint32(n), nil
}

func rejected(reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrRejected
	}
	return fmt.Errorf("%w: %s", ErrRejected, reason)
}

// refusalError names the two reasons the daemon closes a session instead of
// answering it, so the UI can point at a fix rather than a generic failure.
func refusalError(reason string) error {
	low := strings.ToLower(reason)
	switch {
	case strings.Contains(low, "not allowed"), strings.Contains(low, "permission"):
		return fmt.Errorf("%w: %s (add yourself to the engine group: sudo usermod -aG proxybridge $USER, then log in again)", ErrDenied, reason)
	case strings.Contains(low, "too many"):
		return fmt.Errorf("%w: %s", ErrBusy, reason)
	}
	return rejected(reason)
}

func onOff(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func itoa(v uint64) string { return strconv.FormatUint(v, 10) }

// ---- operations ----

// Hello returns the IPC protocol revision the daemon agreed to.
func (c *Client) Hello() (uint32, error) {
	v, _, err := c.hello()
	return v, err
}

func (c *Client) hello() (uint32, string, error) {
	r, err := c.request("hello", strconv.Itoa(linuxIPCProtocol))
	if err != nil {
		return 0, "", err
	}
	if r.kind != "ok" {
		if strings.Contains(strings.ToLower(r.reason), "protocol") {
			return 0, "", fmt.Errorf("%w: %s", ErrBadVer, r.reason)
		}
		return 0, "", rejected(r.reason)
	}
	name := ""
	if len(r.args) > 0 {
		name = r.args[0]
	}
	return linuxIPCProtocol, name, nil
}

// replayHistory pushes the daemon's recent log lines into the event stream, so
// a user who opens Ferry after a reboot sees why the engine did what it did.
func (c *Client) replayHistory() {
	r, err := c.request("log.history")
	if err != nil || r.kind != "ok" {
		return
	}
	for _, line := range r.args {
		if strings.TrimSpace(line) == "" {
			continue
		}
		c.emit(Event{Kind: EvtLog, Log: line})
	}
}

// proxyName is the label the engine shows in its own log and checks.
func proxyName(p ProxyConfig) string {
	if p.IP == "" {
		return p.Type.String()
	}
	return p.IP + ":" + strconv.Itoa(int(p.Port))
}

// AddProxy registers a proxy configuration and returns its engine ID.
func (c *Client) AddProxy(p ProxyConfig) (uint32, error) {
	id, err := c.idCall("proxy.add", proxyName(p), strconv.Itoa(int(p.Type)), p.IP, strconv.Itoa(int(p.Port)), p.User, p.Pass)
	if err != nil {
		return 0, err
	}
	if err := c.okCall("proxy.sendDomain", itoa(uint64(id)), onOff(p.SendDomain)); err != nil {
		return 0, err
	}
	return id, nil
}

// EditProxy changes a proxy configuration in place.
func (c *Client) EditProxy(p ProxyConfig) error {
	if p.ID == 0 {
		return ErrRejected
	}
	if err := c.okCall("proxy.edit", itoa(uint64(p.ID)), proxyName(p), strconv.Itoa(int(p.Type)), p.IP, strconv.Itoa(int(p.Port)), p.User, p.Pass); err != nil {
		return err
	}
	return c.okCall("proxy.sendDomain", itoa(uint64(p.ID)), onOff(p.SendDomain))
}

// DeleteProxy removes a proxy configuration.
func (c *Client) DeleteProxy(id uint32) error {
	return c.okCall("proxy.delete", itoa(uint64(id)))
}

// TestProxy runs a connectivity check. The daemon streams the steps and only
// answers when the check is over, so this returns as soon as the request is on
// the wire and reports progress as EvtTestLine / EvtTestDone events, exactly
// like the Windows service does.
func (c *Client) TestProxy(id uint32, host string, port uint16) error {
	host = strings.TrimSpace(host)
	if host == "" || strings.Contains(host, " ") {
		return fmt.Errorf("%w: the target host is empty", ErrRejected)
	}
	go c.runTest(id, host, port)
	return nil
}

func (c *Client) runTest(id uint32, host string, port uint16) {
	rid, ch, err := c.begin("proxy.test", itoa(uint64(id)), host, strconv.Itoa(int(port)))
	if err != nil {
		c.emitTest(err.Error(), -1)
		return
	}
	defer c.forget(rid)

	deadline := time.After(testTimeout)
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				c.emitTest("the engine connection was lost during the check", -1)
				return
			}
			switch r.kind {
			case "line":
				if len(r.args) > 0 {
					c.emit(Event{Kind: EvtTestLine, TestLine: r.args[0]})
				}
			case "ok":
				rc := int32(0)
				if len(r.args) > 0 {
					n, err := strconv.ParseInt(r.args[0], 10, 32)
					if err != nil {
						n = -1
					}
					rc = int32(n)
				}
				c.emit(Event{Kind: EvtTestDone, TestResult: rc})
				return
			case "err":
				c.emitTest(rejected(r.reason).Error(), -1)
				return
			}
		case r := <-c.refusals:
			c.emitTest(refusalError(r.reason).Error(), -1)
			return
		case <-deadline:
			c.emitTest("the engine did not finish the check in time", -1)
			return
		}
	}
}

func (c *Client) emitTest(text string, rc int32) {
	c.emit(Event{Kind: EvtTestLine, TestLine: text})
	c.emit(Event{Kind: EvtTestDone, TestResult: rc})
}

// AddRule appends a rule and returns its engine ID.
func (c *Client) AddRule(s RuleSpec) (uint32, error) {
	id, err := c.idCall("rule.add", ruleArgs(s)...)
	if err != nil {
		return 0, err
	}
	if err := c.setLocalhost(id, s.Localhost); err != nil {
		return 0, err
	}
	return id, nil
}

// EditRule replaces a rule's payload, keeping its position. Unlike the Windows
// build (which answers 0 and needs a delete/re-add cycle), this engine edits in
// place.
func (c *Client) EditRule(id uint32, s RuleSpec) error {
	args := append([]string{itoa(uint64(id))}, ruleArgs(s)...)
	if err := c.okCall("rule.edit", args...); err != nil {
		return err
	}
	return c.setLocalhost(id, s.Localhost)
}

func (c *Client) setLocalhost(id uint32, on bool) error {
	return c.okCall("rule.localhost", itoa(uint64(id)), onOff(on))
}

// ruleArgs maps a RuleSpec onto the daemon's three filters. Ferry keeps hosts
// and domains apart because the Windows engine expects two fields; this engine
// matches one target list against either, so the two are merged.
func ruleArgs(s RuleSpec) []string {
	return []string{
		orStar(s.Process),
		mergeTargets(s.Hosts, s.Domains),
		orStar(s.Ports),
		strconv.Itoa(int(s.Protocol)),
		strconv.Itoa(int(s.Action)),
		itoa(uint64(s.ProxyID)),
	}
}

func orStar(field *string) string {
	if field == nil {
		return "*"
	}
	v := strings.TrimSpace(*field)
	if v == "" {
		return "*"
	}
	return v
}

func mergeTargets(hosts, domains *string) string {
	var parts []string
	for _, f := range []*string{hosts, domains} {
		if f == nil {
			continue
		}
		for _, part := range strings.FieldsFunc(*f, func(r rune) bool { return r == ',' || r == ';' }) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, ",")
}

// EnableRule / DisableRule toggle a rule without removing it.
func (c *Client) EnableRule(id uint32) error {
	return c.okCall("rule.enable", itoa(uint64(id)))
}

func (c *Client) DisableRule(id uint32) error {
	return c.okCall("rule.disable", itoa(uint64(id)))
}

// DeleteRule removes a rule.
func (c *Client) DeleteRule(id uint32) error {
	return c.okCall("rule.delete", itoa(uint64(id)))
}

// MoveRule moves a rule to a 1-based position.
func (c *Client) MoveRule(id, pos uint32) error {
	return c.okCall("rule.move", itoa(uint64(id)), itoa(uint64(pos)))
}

// RulePosition is not exposed by this engine. The Windows build only calls it
// while replacing a rule, and replacement here edits in place, so it never runs.
func (c *Client) RulePosition(id uint32) (uint32, error) {
	return 0, fmt.Errorf("%w: this engine does not report rule positions", ErrRejected)
}

// SetTrafficLog toggles the engine's per-packet traffic log lines.
func (c *Client) SetTrafficLog(on bool) error {
	return c.okCall("cfg.logging", onOff(on))
}

// ClearConnLogs clears the engine's connection history.
func (c *Client) ClearConnLogs() error {
	return c.okCall("cfg.clearLogs")
}

// Start installs the filter; Stop removes it.
func (c *Client) Start() error { return c.okCall("engine.start") }
func (c *Client) Stop() error  { return c.okCall("engine.stop") }

// SetEnabled pauses or resumes all rules for new connections.
func (c *Client) SetEnabled(on bool) error {
	return c.okCall("cfg.enabled", onOff(on))
}

// SetLogAll logs every new connection instead of de-duplicating.
func (c *Client) SetLogAll(on bool) error {
	return c.okCall("cfg.logEvery", onOff(on))
}

// Flows returns the live proxied connections. The daemon caps the list itself
// at PB_FLOWS_MAX, and it does not report which rule matched a connection.
func (c *Client) Flows(max uint32) ([]Flow, error) {
	if max == 0 || max > MaxFlows {
		max = MaxFlows
	}
	r, err := c.request("flows.get")
	if err != nil {
		return nil, err
	}
	if r.kind != "ok" {
		return nil, rejected(r.reason)
	}
	out := make([]Flow, 0, len(r.args))
	for _, line := range r.args {
		if line == "" {
			continue
		}
		f, err := parseFlow(line)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		if len(out) >= int(max) {
			break
		}
	}
	return out, nil
}

// parseFlow decodes one tab-separated flows.get record:
// id, pid, proxy, age_ms, sent, received, port, udp, ipv6, dest, name, host.
func parseFlow(line string) (Flow, error) {
	parts := strings.Split(line, "\t")
	if len(parts) < 12 {
		return Flow{}, fmt.Errorf("pbproto: flow record has %d fields", len(parts))
	}
	return Flow{
		ID:       parseU64(parts[0]),
		PID:      uint32(parseU64(parts[1])),
		ProxyID:  uint32(parseU64(parts[2])),
		AgeMS:    uint32(parseU64(parts[3])),
		Sent:     parseU64(parts[4]),
		Received: parseU64(parts[5]),
		Port:     uint16(parseU64(parts[6])),
		UDP:      parts[7] == "1",
		IPv6:     parts[8] == "1",
		Name:     parts[10],
		Host:     parts[11],
	}, nil
}

func parseU64(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return v
}
