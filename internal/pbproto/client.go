package pbproto

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// Errors Dial can report, so the UI can tell the user what to do.
var (
	ErrNoService = errors.New("pbproto: the engine service is not installed or not running")
	ErrBusy      = errors.New("pbproto: another client is already connected to the engine")
	ErrDenied    = errors.New("pbproto: access denied by the engine service")
	ErrBadVer    = errors.New("pbproto: unsupported protocol version")
	ErrClosed    = errors.New("pbproto: connection closed")
	ErrTimeout   = errors.New("pbproto: the engine did not answer")
	ErrRejected  = errors.New("pbproto: the engine rejected the request")
	// ErrDisconnected means the control pipe died underneath us. The service
	// closes it when a rule move points past the end of its rule list.
	ErrDisconnected = errors.New("pbproto: the engine connection was lost")
)

// pipeError tags a dead control pipe so callers can tell it apart from a
// request the service refused.
func pipeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windows.ERROR_INVALID_HANDLE) ||
		errors.Is(err, windows.ERROR_NO_DATA) {
		return fmt.Errorf("%w: %v", ErrDisconnected, err)
	}
	return err
}

const callTimeout = 8 * time.Second

// Client is a connection to the engine service. One client at a time, as the
// service allows a single connected GUI.
type Client struct {
	ctl windows.Handle
	evt windows.Handle

	mu   sync.Mutex // serialises request/response exchanges
	once sync.Once

	events  chan Event
	errs    chan error
	version uint32
}

// Dial connects to the installed service.
func Dial() (*Client, error) { return DialPipes(PipeCtl, PipeEvt) }

// DialPipes connects to the given pipe names (the mock service uses the same
// defaults, so this is mainly for tests).
func DialPipes(ctlName, evtName string) (*Client, error) {
	ctl, err := openPipe(ctlName, windows.GENERIC_READ|windows.GENERIC_WRITE, true)
	if err != nil {
		return nil, err
	}
	// The event pipe is created write-only by the service, so it cannot be put
	// in message mode by the client: it is read as a byte stream and framed here.
	evt, err := openPipe(evtName, windows.GENERIC_READ, false)
	if err != nil {
		windows.CloseHandle(ctl)
		return nil, err
	}
	c := &Client{
		ctl:    ctl,
		evt:    evt,
		events: make(chan Event, 1024),
		errs:   make(chan error, 1),
	}
	ver, err := c.Hello()
	if err != nil {
		c.Close()
		return nil, err
	}
	if ver != ProtoVersion {
		c.Close()
		return nil, fmt.Errorf("%w: the service speaks v%d, this app needs v%d", ErrBadVer, ver, ProtoVersion)
	}
	c.version = ver
	go c.readEvents()
	return c, nil
}

func openPipe(name string, access uint32, messageMode bool) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	// ERROR_PIPE_BUSY is retried for a moment: the service answers a client
	// that is closing right now.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		h, err := windows.CreateFile(p, access, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			if messageMode {
				// Message mode keeps one request/response per exchange.
				mode := uint32(windows.PIPE_READMODE_MESSAGE)
				if err := windows.SetNamedPipeHandleState(h, &mode, nil, nil); err != nil {
					windows.CloseHandle(h)
					return windows.InvalidHandle, fmt.Errorf("pbproto: set pipe mode on %s: %w", name, err)
				}
			}
			return h, nil
		}
		switch {
		case errors.Is(err, windows.ERROR_PIPE_BUSY):
			if time.Now().After(deadline) {
				return windows.InvalidHandle, ErrBusy
			}
			time.Sleep(50 * time.Millisecond)
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
			return windows.InvalidHandle, ErrNoService
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return windows.InvalidHandle, ErrDenied
		default:
			return windows.InvalidHandle, err
		}
	}
}

// Version is the protocol version agreed at dial time.
func (c *Client) Version() uint32 { return c.version }

// Events carries engine events. It is closed when the connection ends.
func (c *Client) Events() <-chan Event { return c.events }

// Errors receives the reason the connection ended, at most once.
func (c *Client) Errors() <-chan error { return c.errs }

func (c *Client) readEvents() {
	defer close(c.events)
	buf := make([]byte, 4096)
	pending := make([]byte, 0, 8192)
	for {
		n, err := readMsg(c.evt, buf)
		if err != nil {
			c.once.Do(func() {
				windows.CloseHandle(c.ctl)
				windows.CloseHandle(c.evt)
			})
			select {
			case c.errs <- err:
			default:
			}
			return
		}
		pending = append(pending, buf[:n]...)
		for len(pending) > 0 {
			ev, used, err := parseEventAt(pending)
			if errors.Is(err, errNeedMore) {
				break
			}
			if err != nil {
				pending = pending[:0] // resynchronise on the next read
				break
			}
			pending = pending[used:]
			select {
			case c.events <- ev:
			default: // the UI is behind: drop, it polls flows anyway
			}
		}
		if len(pending) > 1<<20 {
			pending = pending[:0]
		}
	}
}

func readMsg(h windows.Handle, buf []byte) (uint32, error) {
	var n uint32
	err := windows.ReadFile(h, buf, &n, nil)
	return n, err
}

// call sends one request and reads one response message.
func (c *Client) call(op Op, body func(*enc)) ([]byte, error) {
	e := &enc{}
	e.u32(uint32(op))
	if body != nil {
		body(e)
	}
	if e.err != nil {
		return nil, e.err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if err := windows.WriteFile(c.ctl, e.b, nil, nil); err != nil {
			ch <- result{err: err}
			return
		}
		buf := make([]byte, msgMax)
		n, err := readMsg(c.ctl, buf)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{b: buf[:n]}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return nil, pipeError(r.err)
		}
		return r.b, nil
	case <-time.After(callTimeout):
		c.Close() // unblocks the goroutine above
		return nil, ErrTimeout
	}
}

// callRet is call() for responses that start with a u32 result.
func (c *Client) callRet(op Op, body func(*enc)) (uint32, error) {
	b, err := c.call(op, body)
	if err != nil {
		return 0, err
	}
	d := &dec{b: b}
	v := d.u32()
	if d.err != nil {
		return 0, d.err
	}
	return v, nil
}

// callBool maps a 0/1 result to an error.
func (c *Client) callBool(op Op, body func(*enc)) error {
	v, err := c.callRet(op, body)
	if err != nil {
		return err
	}
	if v == 0 {
		return ErrRejected
	}
	return nil
}

// Close ends the connection. Safe to call more than once.
func (c *Client) Close() error {
	c.once.Do(func() {
		if c.ctl != windows.InvalidHandle {
			windows.CloseHandle(c.ctl)
		}
		if c.evt != windows.InvalidHandle {
			windows.CloseHandle(c.evt)
		}
	})
	return nil
}

// ---- operations ----

// Hello returns the service's protocol version.
func (c *Client) Hello() (uint32, error) {
	return c.callRet(OpHello, nil)
}

// AddProxy registers a proxy configuration and returns its engine ID.
func (c *Client) AddProxy(p ProxyConfig) (uint32, error) {
	id, err := c.callRet(OpAddProxy, func(e *enc) {
		e.u32(uint32(p.Type))
		e.strVal(p.IP)
		e.u32(uint32(p.Port))
		e.str(optStr(p.User))
		e.str(optStr(p.Pass))
		e.u32(boolU32(p.SendDomain))
	})
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, ErrRejected
	}
	return id, nil
}

// EditProxy changes a proxy configuration in place.
func (c *Client) EditProxy(p ProxyConfig) error {
	return c.callBool(OpEditProxy, func(e *enc) {
		e.u32(p.ID)
		e.u32(uint32(p.Type))
		e.strVal(p.IP)
		e.u32(uint32(p.Port))
		e.str(optStr(p.User))
		e.str(optStr(p.Pass))
		e.u32(boolU32(p.SendDomain))
	})
}

// DeleteProxy removes a proxy configuration.
func (c *Client) DeleteProxy(id uint32) error {
	return c.callBool(OpDeleteProxy, func(e *enc) { e.u32(id) })
}

// TestProxy starts a streaming check. Lines and the verdict arrive as
// EvtTestLine / EvtTestDone events.
func (c *Client) TestProxy(id uint32, host string, port uint16) error {
	return c.callBool(OpTestProxyStart, func(e *enc) {
		e.u32(id)
		e.strVal(host)
		e.u32(uint32(port))
	})
}

// AddRule appends a rule and returns its engine ID.
func (c *Client) AddRule(s RuleSpec) (uint32, error) {
	id, err := c.callRet(OpAddRule, func(e *enc) { writeRule(e, 0, s) })
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, ErrRejected
	}
	return id, nil
}

// EditRule replaces a rule's payload, keeping its position.
func (c *Client) EditRule(id uint32, s RuleSpec) error {
	return c.callBool(OpEditRule, func(e *enc) { writeRule(e, id, s) })
}

func writeRule(e *enc, id uint32, s RuleSpec) {
	if id != 0 {
		e.u32(id)
	}
	e.str(s.Process)
	e.str(s.Hosts)
	e.str(s.Ports)
	e.str(s.Domains)
	e.u32(uint32(s.Protocol))
	e.u32(uint32(s.Action))
	e.u32(s.ProxyID)
	e.u32(boolU32(s.Localhost))
}

// EnableRule / DisableRule toggle a rule without removing it.
func (c *Client) EnableRule(id uint32) error {
	return c.callBool(OpEnableRule, func(e *enc) { e.u32(id) })
}

func (c *Client) DisableRule(id uint32) error {
	return c.callBool(OpDisableRule, func(e *enc) { e.u32(id) })
}

// DeleteRule removes a rule.
func (c *Client) DeleteRule(id uint32) error {
	return c.callBool(OpDeleteRule, func(e *enc) { e.u32(id) })
}

// MoveRule moves a rule to a 1-based position.
func (c *Client) MoveRule(id, pos uint32) error {
	return c.callBool(OpMoveRule, func(e *enc) {
		e.u32(id)
		e.u32(pos)
	})
}

// RulePosition returns the 1-based position of a rule.
func (c *Client) RulePosition(id uint32) (uint32, error) {
	return c.callRet(OpGetRulePos, func(e *enc) { e.u32(id) })
}

// SetTrafficLog toggles the engine's per-packet traffic log lines.
func (c *Client) SetTrafficLog(on bool) error {
	_, err := c.callRet(OpSetTrafficLog, func(e *enc) { e.u32(boolU32(on)) })
	return err
}

// ClearConnLogs clears the engine's connection history.
func (c *Client) ClearConnLogs() error {
	_, err := c.callRet(OpClearConnLogs, nil)
	return err
}

// Start installs the packet filter; Stop removes it.
func (c *Client) Start() error { return c.callBool(OpStart, nil) }
func (c *Client) Stop() error  { return c.callBool(OpStop, nil) }

// SetEnabled pauses or resumes all rules for new connections.
func (c *Client) SetEnabled(on bool) error {
	_, err := c.callRet(OpSetEnabled, func(e *enc) { e.u32(boolU32(on)) })
	return err
}

// SetLogAll logs every new connection instead of de-duplicating.
func (c *Client) SetLogAll(on bool) error {
	_, err := c.callRet(OpSetLogAll, func(e *enc) { e.u32(boolU32(on)) })
	return err
}

// Flows returns the live proxied connections, newest first.
func (c *Client) Flows(max uint32) ([]Flow, error) {
	if max == 0 || max > MaxFlows {
		max = MaxFlows
	}
	b, err := c.call(OpGetFlows, func(e *enc) { e.u32(max) })
	if err != nil {
		return nil, err
	}
	return parseFlows(b)
}

func boolU32(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

// optStr is nil for an empty string so the service sends NULL, which the
// engine reads as "no restriction".
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
