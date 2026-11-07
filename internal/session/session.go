// Package session owns the connection to the engine, replays the app's rule
// list into it and keeps the ring buffers the UI polls.
package session

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"pbdeck/internal/model"
	"pbdeck/internal/pbproto"
	"pbdeck/internal/procattr"
)

// Modes reported to the UI.
const (
	ModeOffline = "offline"
	ModeService = "service"
	ModeMock    = "mock"
)

// Event is one row of the activity log: a new connection, an engine message or
// a line of a proxy test.
type Event struct {
	Seq      uint64    `json:"seq"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"` // conn | log | test
	Process  string    `json:"process,omitempty"`
	PID      uint32    `json:"pid,omitempty"`
	Host     string    `json:"host,omitempty"`
	Port     uint16    `json:"port,omitempty"`
	Info     string    `json:"info,omitempty"`
	Text     string    `json:"text,omitempty"`
	RuleKey  string    `json:"ruleKey,omitempty"`
	ProxyKey string    `json:"proxyKey,omitempty"`
}

// FlowView is a live connection plus the keys that explain it.
type FlowView struct {
	pbproto.Flow
	RuleKey   string `json:"ruleKey,omitempty"`
	ProxyKey  string `json:"proxyKey,omitempty"`
	ProxyName string `json:"proxyName,omitempty"`
}

// TestRun is the state of the last connectivity test.
type TestRun struct {
	ProxyKey string    `json:"proxyKey"`
	Target   string    `json:"target"`
	Lines    []string  `json:"lines"`
	Running  bool      `json:"running"`
	Result   *int32    `json:"result,omitempty"`
	Started  time.Time `json:"started"`
}

// Stats are session counters for the status bar.
type Stats struct {
	Events  int    `json:"events"`
	Flows   int    `json:"flows"`
	Up      uint64 `json:"up"`
	Down    uint64 `json:"down"`
	Replays int    `json:"replays"`
}

// View is everything the page needs in one poll.
type View struct {
	Mode       string        `json:"mode"`
	Connected  bool          `json:"connected"`
	Version    uint32        `json:"version"`
	Error      string        `json:"error,omitempty"`
	Enabled    bool          `json:"enabled"`
	TrafficLog bool          `json:"trafficLog"`
	LogAll     bool          `json:"logAll"`
	Proxies    []model.Proxy `json:"proxies"`
	Rules      []model.Rule  `json:"rules"`
	Flows      []FlowView    `json:"flows"`
	Events     []Event       `json:"events"`
	Seq        uint64        `json:"seq"`
	Test       *TestRun      `json:"test,omitempty"`
	Stats      Stats         `json:"stats"`
	Started    time.Time     `json:"started"`
}

// RuleTarget is what the page sends back for a right-clicked row: a live flow
// or a connection-log line.
type RuleTarget struct {
	FlowID   uint64 `json:"flowId,omitempty"`
	Process  string `json:"process"`
	PID      uint32 `json:"pid"`
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	UDP      bool   `json:"udp"`
	ProxyKey string `json:"proxyKey,omitempty"`
	// MatchedRuleKey is the rule that let this connection through, so the new
	// rule can be inserted right above it.
	MatchedRuleKey string `json:"matchedRuleKey,omitempty"`
}

// RuleOpts is how a log row becomes a rule.
type RuleOpts struct {
	// Scope is "host" (this destination), "process" (everything this app
	// connects to) or "process-host" (this app to any host, this port).
	Scope     string             `json:"scope"`
	Action    pbproto.RuleAction `json:"action"`
	ProxyKey  string             `json:"proxyKey"`
	Ports     string             `json:"ports"`
	Localhost bool               `json:"localhost"`
	MoveToTop bool               `json:"moveToTop"`
}

// RuleResult reports what one right-click action did.
type RuleResult struct {
	FlowID   uint64 `json:"flowId"`
	RuleKey  string `json:"ruleKey"`
	Process  string `json:"process"`
	Created  bool   `json:"created"`
	Merged   bool   `json:"merged"`
	Position int    `json:"position"`
	Summary  string `json:"summary"`
}

const (
	maxEvents = 800
	maxFlows  = 400
)

// Session is the live connection and its buffers.
type Session struct {
	store *model.Store

	mu      sync.Mutex
	cli     *pbproto.Client
	mock    *pbproto.Mock
	mode    string
	lastErr string
	version uint32
	flows   []FlowView
	events  []Event
	seq     uint64
	test    *TestRun
	stats   Stats
	stop    chan struct{}
	started time.Time
	logf    func(string, ...any)
}

// New creates an offline session over the given store.
func New(st *model.Store, logf func(string, ...any)) *Session {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Session{store: st, mode: ModeOffline, logf: logf}
}

// Mode is the current connection kind.
func (s *Session) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// Connect dials the engine (or the mock) and replays the stored rules.
func (s *Session) Connect(useMock bool) error {
	// If a switch to the real engine fails, the demo session is put back so a
	// missing service does not leave the UI empty.
	prev := s.Mode()
	s.Disconnect()

	var cli *pbproto.Client
	var mock *pbproto.Mock
	var err error
	mode := ModeService

	if useMock {
		mode = ModeMock
		mock = pbproto.NewMock()
		if err := mock.Start(); err != nil {
			return fmt.Errorf("mock engine: %w", err)
		}
		cli, err = pbproto.DialPipes(mock.CtlName, mock.EvtName)
		if err != nil {
			mock.Close()
			return fmt.Errorf("mock engine: %w", err)
		}
	} else {
		cli, err = pbproto.Dial()
		if err != nil {
			if prev == ModeMock {
				_ = s.Connect(true)
				return fmt.Errorf("%w（已留在演示引擎）", err)
			}
			return err
		}
	}

	stop := make(chan struct{})
	s.mu.Lock()
	s.cli = cli
	s.mock = mock
	s.mode = mode
	s.stop = stop
	s.version = cli.Version()
	s.started = time.Now()
	s.lastErr = ""
	s.events = nil
	s.flows = nil
	s.seq = 0
	s.stats = Stats{}
	s.test = nil
	s.mu.Unlock()

	if err := s.replay(); err != nil {
		s.setError(err)
	}

	go s.pump(cli, stop)
	go s.pollFlows(cli, stop)
	s.logf("connected to %s engine (protocol v%d)", mode, cli.Version())
	return nil
}

// Disconnect tears the connection down; buffers stay for the offline view.
func (s *Session) Disconnect() {
	s.mu.Lock()
	cli, mock, stop := s.cli, s.mock, s.stop
	was := s.mode
	s.cli, s.mock, s.stop = nil, nil, nil
	s.mode = ModeOffline
	s.flows = nil
	if s.test != nil && s.test.Running {
		s.test.Running = false
	}
	s.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if cli != nil {
		_ = cli.Close()
	}
	if mock != nil {
		mock.Close()
	}
	if was != ModeOffline {
		s.logf("disconnected from %s engine", was)
	}
}

func (s *Session) setError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
	s.logf("engine: %v", err)
	s.appendEvent(Event{Kind: "log", Text: "引擎错误：" + err.Error()})
}

// replay pushes the stored proxies and rules into a fresh engine. The service
// starts a new engine per client, so this runs on every connect.
func (s *Session) replay() error {
	st := s.store.Snapshot()

	proxyIDs := make(map[string]uint32, len(st.Proxies))
	for _, p := range st.Proxies {
		id, err := s.cli.AddProxy(pbproto.ProxyConfig{
			Type: p.Type, IP: p.IP, Port: p.Port, User: p.User, Pass: p.Pass, SendDomain: p.SendDomain,
		})
		if err != nil {
			return fmt.Errorf("proxy %q: %w", p.Name, err)
		}
		proxyIDs[p.Key] = id
		s.setProxyEngineID(p.Key, id)
	}

	for i, r := range st.Rules {
		spec := pbproto.RuleSpec{
			Process:   optStr(r.Process),
			Hosts:     optStr(r.Hosts),
			Ports:     optStr(r.Ports),
			Domains:   optStr(r.Domains),
			Protocol:  r.Protocol,
			Action:    r.Action,
			ProxyID:   proxyIDs[r.ProxyKey],
			Localhost: r.Localhost,
		}
		id, err := s.cli.AddRule(spec)
		if err != nil {
			return fmt.Errorf("rule %d (%s): %w", i+1, ruleSummary(r), err)
		}
		s.setRuleEngineID(r.Key, id)
		if !r.Enabled {
			if err := s.cli.DisableRule(id); err != nil {
				return fmt.Errorf("disable rule %d: %w", i+1, err)
			}
		}
	}

	st = s.store.Snapshot()
	if err := s.cli.SetEnabled(st.Engine.Enabled); err != nil {
		return fmt.Errorf("set enabled: %w", err)
	}
	if err := s.cli.SetTrafficLog(st.Engine.TrafficLog); err != nil {
		return fmt.Errorf("set traffic log: %w", err)
	}
	if err := s.cli.SetLogAll(st.Engine.LogAll); err != nil {
		return fmt.Errorf("set log all: %w", err)
	}
	if err := s.cli.Start(); err != nil {
		// A missing WinDivert driver is the usual cause; the rules are still
		// editable, so report it without dropping the connection.
		return fmt.Errorf("start capture: %w", err)
	}

	s.mu.Lock()
	s.stats.Replays++
	s.mu.Unlock()
	return nil
}

func optStr(s string) *string {
	if s == "" || s == "*" {
		return nil
	}
	return &s
}

func (s *Session) setProxyEngineID(key string, id uint32) {
	_ = s.store.Update(func(st *model.Settings) error {
		for i := range st.Proxies {
			if st.Proxies[i].Key == key {
				st.Proxies[i].EngineID = id
			}
		}
		return nil
	})
}

func (s *Session) setRuleEngineID(key string, id uint32) {
	_ = s.store.Update(func(st *model.Settings) error {
		for i := range st.Rules {
			if st.Rules[i].Key == key {
				st.Rules[i].EngineID = id
			}
		}
		return nil
	})
}

// Snapshot returns the current view; events after `since` are included.
func (s *Session) Snapshot(since uint64) View {
	st := s.store.Snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()

	v := View{
		Mode:       s.mode,
		Connected:  s.cli != nil,
		Version:    s.version,
		Error:      s.lastErr,
		Enabled:    st.Engine.Enabled,
		TrafficLog: st.Engine.TrafficLog,
		LogAll:     st.Engine.LogAll,
		Proxies:    st.Proxies,
		Rules:      st.Rules,
		Seq:        s.seq,
		Stats:      s.stats,
		Started:    s.started,
	}
	v.Flows = append([]FlowView(nil), s.flows...)
	for _, e := range s.events {
		if e.Seq > since {
			v.Events = append(v.Events, e)
		}
	}
	if s.test != nil {
		t := *s.test
		t.Lines = append([]string(nil), s.test.Lines...)
		v.Test = &t
	}
	return v
}

// ---- engine switches ----

// SetEnabled pauses or resumes matching for new connections.
func (s *Session) SetEnabled(on bool) error {
	if err := s.engine(func(c *pbproto.Client) error { return c.SetEnabled(on) }); err != nil {
		return err
	}
	return s.store.Update(func(st *model.Settings) error { st.Engine.Enabled = on; return nil })
}

// SetTrafficLog toggles per-packet traffic lines in the engine log.
func (s *Session) SetTrafficLog(on bool) error {
	if err := s.engine(func(c *pbproto.Client) error { return c.SetTrafficLog(on) }); err != nil {
		return err
	}
	return s.store.Update(func(st *model.Settings) error { st.Engine.TrafficLog = on; return nil })
}

// SetLogAll logs every connection instead of collapsing repeats.
func (s *Session) SetLogAll(on bool) error {
	if err := s.engine(func(c *pbproto.Client) error { return c.SetLogAll(on) }); err != nil {
		return err
	}
	return s.store.Update(func(st *model.Settings) error { st.Engine.LogAll = on; return nil })
}

// engine runs fn with the live client. Offline it is a no-op so edits still
// work and are replayed on the next connect.
func (s *Session) engine(fn func(*pbproto.Client) error) error {
	s.mu.Lock()
	cli := s.cli
	s.mu.Unlock()
	if cli == nil {
		return nil
	}
	return fn(cli)
}

// ClearEvents empties the activity ring and asks the engine to drop its own
// connection history.
func (s *Session) ClearEvents() error {
	s.mu.Lock()
	s.events = nil
	s.seq = 0
	s.stats = Stats{}
	s.mu.Unlock()
	return s.engine(func(c *pbproto.Client) error { return c.ClearConnLogs() })
}

// ---- proxies ----

// SaveProxy adds (empty Key) or edits a proxy configuration.
func (s *Session) SaveProxy(p model.Proxy) (model.Proxy, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name = p.IP + ":" + strconv.Itoa(int(p.Port))
	}
	if p.IP == "" || p.Port == 0 {
		return p, errors.New("代理地址和端口不能为空")
	}

	var out model.Proxy
	err := s.store.Update(func(st *model.Settings) error {
		if p.Key == "" {
			p.Key = model.NewKey()
			st.Proxies = append(st.Proxies, p)
			out = p
			return nil
		}
		for i := range st.Proxies {
			if st.Proxies[i].Key == p.Key {
				p.EngineID = st.Proxies[i].EngineID
				st.Proxies[i] = p
				out = p
				return nil
			}
		}
		return errors.New("代理不存在")
	})
	if err != nil {
		return p, err
	}

	err = s.engine(func(c *pbproto.Client) error {
		cfg := pbproto.ProxyConfig{Type: out.Type, IP: out.IP, Port: out.Port, User: out.User, Pass: out.Pass, SendDomain: out.SendDomain}
		if out.EngineID == 0 {
			id, err := c.AddProxy(cfg)
			if err != nil {
				return err
			}
			s.setProxyEngineID(out.Key, id)
			out.EngineID = id
			return nil
		}
		cfg.ID = out.EngineID
		return c.EditProxy(cfg)
	})
	if err != nil {
		s.setError(err)
	}
	return out, err
}

// DeleteProxy removes a proxy; rules pointing at it fall back to the first.
func (s *Session) DeleteProxy(key string) error {
	var engineID uint32
	err := s.store.Update(func(st *model.Settings) error {
		for i, p := range st.Proxies {
			if p.Key == key {
				engineID = p.EngineID
				st.Proxies = append(st.Proxies[:i], st.Proxies[i+1:]...)
				break
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = s.engine(func(c *pbproto.Client) error {
		if engineID == 0 {
			return nil
		}
		return c.DeleteProxy(engineID)
	})
	if err != nil {
		s.setError(err)
	}
	return err
}

// TestProxy runs the engine's connectivity test and streams its lines into the
// view.
func (s *Session) TestProxy(key string) error {
	st := s.store.Snapshot()
	var p *model.Proxy
	for i := range st.Proxies {
		if st.Proxies[i].Key == key {
			p = &st.Proxies[i]
			break
		}
	}
	if p == nil {
		return errors.New("代理不存在")
	}
	if p.EngineID == 0 {
		return errors.New("先连接引擎再测试代理")
	}

	s.mu.Lock()
	s.test = &TestRun{ProxyKey: key, Target: fmt.Sprintf("%s:%d", p.IP, p.Port), Running: true, Started: time.Now()}
	s.mu.Unlock()

	err := s.engine(func(c *pbproto.Client) error { return c.TestProxy(p.EngineID, "", 0) })
	if err != nil {
		s.mu.Lock()
		if s.test != nil {
			s.test.Running = false
			s.test.Lines = append(s.test.Lines, "测试无法开始："+err.Error())
		}
		s.mu.Unlock()
		return err
	}

	go func(started time.Time) {
		time.Sleep(25 * time.Second)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.test != nil && s.test.Running && s.test.Started.Equal(started) {
			s.test.Running = false
			s.test.Lines = append(s.test.Lines, "测试超时。")
		}
	}(s.test.Started)
	return nil
}

// ---- rules ----

// SaveRule adds or edits a rule. insertAt > 0 asks the engine for that 1-based
// position; 0 keeps the current place (new rules go to the end).
func (s *Session) SaveRule(r model.Rule, insertAt int) (model.Rule, error) {
	r.Process = strings.TrimSpace(r.Process)
	if r.Process == "" {
		r.Process = "*"
	}
	if r.Ports == "" {
		r.Ports = "*"
	}
	if r.Hosts == "" {
		r.Hosts = "*"
	}
	r.Domains = strings.TrimSpace(r.Domains)
	if r.Domains != "" {
		r.Hosts = "*" // a domain rule constrains the name, not the IP
	}

	var out model.Rule
	err := s.store.Update(func(st *model.Settings) error {
		if r.Key == "" {
			r.Key = model.NewKey()
			r.Enabled = true
			if insertAt <= 0 || insertAt > len(st.Rules)+1 {
				insertAt = len(st.Rules) + 1
			}
			st.Rules = append(st.Rules, r)
			copy(st.Rules[insertAt:], st.Rules[insertAt-1:])
			st.Rules[insertAt-1] = r
			out = r
			return nil
		}
		for i := range st.Rules {
			if st.Rules[i].Key == r.Key {
				r.EngineID = st.Rules[i].EngineID
				st.Rules[i] = r
				out = r
				return nil
			}
		}
		return errors.New("规则不存在")
	})
	if err != nil {
		return r, err
	}

	err = s.engine(func(c *pbproto.Client) error {
		spec := ruleSpec(s.store.Snapshot(), out)
		if out.EngineID == 0 {
			id, err := c.AddRule(spec)
			if err != nil {
				return err
			}
			s.setRuleEngineID(out.Key, id)
			out.EngineID = id
			if insertAt > 0 {
				if err := c.MoveRule(id, uint32(insertAt)); err != nil {
					return err
				}
			}
			return nil
		}
		if err := c.EditRule(out.EngineID, spec); err != nil {
			return err
		}
		if out.Enabled {
			if err := c.EnableRule(out.EngineID); err != nil {
				return err
			}
		} else {
			if err := c.DisableRule(out.EngineID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.setError(err)
	}
	return out, err
}

func ruleSpec(st model.Settings, r model.Rule) pbproto.RuleSpec {
	var proxyID uint32
	for _, p := range st.Proxies {
		if p.Key == r.ProxyKey {
			proxyID = p.EngineID
			break
		}
	}
	return pbproto.RuleSpec{
		Process:   optStr(r.Process),
		Hosts:     optStr(r.Hosts),
		Ports:     optStr(r.Ports),
		Domains:   optStr(r.Domains),
		Protocol:  r.Protocol,
		Action:    r.Action,
		ProxyID:   proxyID,
		Localhost: r.Localhost,
	}
}

// DeleteRule removes a rule from the store and the engine.
func (s *Session) DeleteRule(key string) error {
	var engineID uint32
	err := s.store.Update(func(st *model.Settings) error {
		for i, r := range st.Rules {
			if r.Key == key {
				engineID = r.EngineID
				st.Rules = append(st.Rules[:i], st.Rules[i+1:]...)
				break
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = s.engine(func(c *pbproto.Client) error {
		if engineID == 0 {
			return nil
		}
		return c.DeleteRule(engineID)
	})
	if err != nil {
		s.setError(err)
	}
	return err
}

// ToggleRule enables or disables a rule.
func (s *Session) ToggleRule(key string, on bool) error {
	var engineID uint32
	err := s.store.Update(func(st *model.Settings) error {
		for i := range st.Rules {
			if st.Rules[i].Key == key {
				st.Rules[i].Enabled = on
				engineID = st.Rules[i].EngineID
				return nil
			}
		}
		return errors.New("规则不存在")
	})
	if err != nil {
		return err
	}
	return s.engine(func(c *pbproto.Client) error {
		if engineID == 0 {
			return nil
		}
		if on {
			return c.EnableRule(engineID)
		}
		return c.DisableRule(engineID)
	})
}

// Reorder applies a new rule order (keys, top first).
func (s *Session) Reorder(keys []string) error {
	var ids []uint32
	err := s.store.Update(func(st *model.Settings) error {
		byKey := make(map[string]model.Rule, len(st.Rules))
		for _, r := range st.Rules {
			byKey[r.Key] = r
		}
		next := make([]model.Rule, 0, len(keys))
		for _, k := range keys {
			if r, ok := byKey[k]; ok {
				next = append(next, r)
				delete(byKey, k)
			}
		}
		for _, r := range st.Rules {
			if _, ok := byKey[r.Key]; ok {
				next = append(next, r)
				delete(byKey, r.Key)
			}
		}
		st.Rules = next
		ids = make([]uint32, len(next))
		for i, r := range next {
			ids[i] = r.EngineID
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.engine(func(c *pbproto.Client) error {
		for i, id := range ids {
			if id == 0 {
				continue
			}
			if err := c.MoveRule(id, uint32(i+1)); err != nil {
				return err
			}
		}
		return nil
	})
}

// RulesFromTargets is the right-click action: turn selected connections into
// rules, inserted above the rule that currently matches them so the new rule
// wins.
func (s *Session) RulesFromTargets(targets []RuleTarget, opts RuleOpts) ([]RuleResult, error) {
	if len(targets) == 0 {
		return nil, errors.New("没有选中连接")
	}
	results := make([]RuleResult, 0, len(targets))
	for _, t := range targets {
		res, err := s.ruleFromTarget(t, opts)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (s *Session) ruleFromTarget(t RuleTarget, opts RuleOpts) (RuleResult, error) {
	name := t.Process
	if name == "" || len(name) >= 39 {
		if n := procattr.NameOf(t.PID); n != "" {
			name = n
		}
	}
	if name == "" {
		name = "*"
	}
	host := t.Host
	isDomain := host != "" && !isIP(host)
	port := strconv.Itoa(int(t.Port))
	proto := pbproto.ProtoTCP
	if t.UDP {
		proto = pbproto.ProtoUDP
	}

	r := model.Rule{
		Process:  name,
		Action:   opts.Action,
		ProxyKey: opts.ProxyKey,
		Protocol: proto,
		Enabled:  true,
		Ports:    port,
	}
	switch opts.Scope {
	case "process":
		r.Hosts, r.Ports = "*", "*"
		r.Protocol = pbproto.ProtoBoth
	case "process-host":
		r.Hosts = "*"
		if opts.Ports != "" {
			r.Ports = opts.Ports
		}
		r.Domains = hostByKind(host, isDomain)
	default: // host
		r.Domains = hostByKind(host, isDomain)
		if opts.Ports != "" {
			r.Ports = opts.Ports
		}
	}
	if r.Action == pbproto.ActionProxy && r.ProxyKey == "" {
		r.ProxyKey = t.ProxyKey
	}
	r.Localhost = opts.Localhost
	r.Note = "来自连接日志"

	summary := describeRule(r)

	// An identical rule already exists: enable it and move it up instead of
	// piling duplicates on top of each other.
	var existing model.Rule
	err := s.store.Update(func(st *model.Settings) error {
		for i := range st.Rules {
			if sameRule(st.Rules[i], r) {
				existing = st.Rules[i]
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return RuleResult{}, err
	}
	matchedID := s.engineIDOfRule(t.MatchedRuleKey)
	if existing.Key != "" {
		r = existing
		r.Enabled = true
		pos, err := s.moveAbove(r, matchedID, opts.MoveToTop)
		if err != nil {
			return RuleResult{}, err
		}
		return RuleResult{FlowID: t.FlowID, RuleKey: r.Key, Process: name, Merged: true, Position: pos, Summary: summary}, nil
	}

	insertAt := 0
	if !opts.MoveToTop {
		insertAt = s.positionOfEngineRule(matchedID)
	}
	saved, err := s.SaveRule(r, insertAt)
	if err != nil {
		return RuleResult{}, err
	}
	pos := 0
	st := s.store.Snapshot()
	for i, rr := range st.Rules {
		if rr.Key == saved.Key {
			pos = i + 1
			break
		}
	}
	return RuleResult{FlowID: t.FlowID, RuleKey: saved.Key, Process: name, Created: true, Position: pos, Summary: summary}, nil
}

func (s *Session) engineIDOfRule(key string) uint32 {
	if key == "" {
		return 0
	}
	for _, r := range s.store.Snapshot().Rules {
		if r.Key == key {
			return r.EngineID
		}
	}
	return 0
}

// moveAbove puts an existing rule right above the rule that matched the flow.
func (s *Session) moveAbove(r model.Rule, matchedEngineID uint32, toTop bool) (int, error) {
	st := s.store.Snapshot()
	cur := 0
	for i, rr := range st.Rules {
		if rr.Key == r.Key {
			cur = i + 1
			break
		}
	}
	target := 1
	if !toTop {
		if p := s.positionOfEngineRule(matchedEngineID); p > 0 {
			target = p
			if cur < target {
				target--
			}
			if target < 1 {
				target = 1
			}
		}
	}
	if cur == target {
		return cur, s.ToggleRule(r.Key, true)
	}
	if cur > 0 {
		keys := ruleKeys(st.Rules)
		keys = moveKey(keys, r.Key, target-1)
		if err := s.Reorder(keys); err != nil {
			return cur, err
		}
		return target, s.ToggleRule(r.Key, true)
	}
	return cur, nil
}

func (s *Session) positionOfEngineRule(engineID uint32) int {
	if engineID == 0 {
		return 0
	}
	for i, r := range s.store.Snapshot().Rules {
		if r.EngineID == engineID {
			return i + 1
		}
	}
	return 0
}

func hostByKind(host string, isDomain bool) string {
	if isDomain {
		return host
	}
	return ""
}

func isIP(host string) bool {
	if host == "" {
		return false
	}
	if strings.Contains(host, ":") {
		return true // IPv6 literal
	}
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func sameRule(a, b model.Rule) bool {
	return strings.EqualFold(strings.TrimSpace(a.Process), strings.TrimSpace(b.Process)) &&
		strings.EqualFold(a.Hosts, b.Hosts) &&
		strings.EqualFold(a.Ports, b.Ports) &&
		strings.EqualFold(a.Domains, b.Domains) &&
		a.Protocol == b.Protocol && a.Action == b.Action && a.ProxyKey == b.ProxyKey
}

func ruleKeys(rs []model.Rule) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Key
	}
	return out
}

func moveKey(keys []string, key string, to int) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	if to < 0 {
		to = 0
	}
	if to > len(out) {
		to = len(out)
	}
	out = append(out, "")
	copy(out[to+1:], out[to:])
	out[to] = key
	return out
}

func describeRule(r model.Rule) string {
	action := "直连"
	switch r.Action {
	case pbproto.ActionProxy:
		action = "走代理"
	case pbproto.ActionBlock:
		action = "阻断"
	}
	where := r.Domains
	if where == "" && r.Hosts != "" && r.Hosts != "*" {
		where = r.Hosts
	}
	if where == "" {
		where = "所有主机"
	}
	if r.Ports != "" && r.Ports != "*" {
		where += ":" + r.Ports
	}
	return fmt.Sprintf("%s → %s %s", r.Process, where, action)
}

func ruleSummary(r model.Rule) string { return describeRule(r) }

// ---- background loops ----

func (s *Session) pump(cli *pbproto.Client, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case ev, ok := <-cli.Events():
			if !ok {
				return
			}
			s.handle(ev)
		case err, ok := <-cli.Errors():
			if !ok {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			s.setError(err)
			return
		}
	}
}

func (s *Session) handle(ev pbproto.Event) {
	switch ev.Kind {
	case pbproto.EvtConn:
		c := ev.Conn
		e := Event{
			Kind: "conn", Process: c.Process, PID: c.PID, Host: c.IP, Port: c.Port, Info: c.Info,
		}
		s.mu.Lock()
		for _, f := range s.flows {
			if f.PID == c.PID && f.Port == c.Port && (f.Host == c.IP || f.Host == "" || isIP(c.IP)) {
				e.RuleKey, e.ProxyKey = f.RuleKey, f.ProxyKey
				break
			}
		}
		s.mu.Unlock()
		s.appendEvent(e)
	case pbproto.EvtLog:
		s.appendEvent(Event{Kind: "log", Text: ev.Log})
	case pbproto.EvtTestLine:
		s.mu.Lock()
		if s.test != nil {
			s.test.Lines = append(s.test.Lines, ev.TestLine)
			if len(s.test.Lines) > 200 {
				s.test.Lines = s.test.Lines[len(s.test.Lines)-200:]
			}
		}
		s.mu.Unlock()
	case pbproto.EvtTestDone:
		s.mu.Lock()
		if s.test != nil {
			s.test.Running = false
			res := ev.TestResult
			s.test.Result = &res
		}
		s.mu.Unlock()
	}
}

func (s *Session) appendEvent(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e.Seq = s.seq
	e.Time = time.Now()
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-maxEvents:]...)
	}
	s.stats.Events++
}

func (s *Session) pollFlows(cli *pbproto.Client, stop chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			flows, err := cli.Flows(maxFlows)
			if err != nil {
				continue
			}
			st := s.store.Snapshot()
			ruleKey := make(map[uint32]string, len(st.Rules))
			for _, r := range st.Rules {
				if r.EngineID != 0 {
					ruleKey[r.EngineID] = r.Key
				}
			}
			proxyByID := make(map[uint32]model.Proxy, len(st.Proxies))
			for _, p := range st.Proxies {
				proxyByID[p.EngineID] = p
			}

			out := make([]FlowView, 0, len(flows))
			var up, down uint64
			for _, f := range flows {
				fv := FlowView{Flow: f, RuleKey: ruleKey[f.RuleID]}
				if p, ok := proxyByID[f.ProxyID]; ok {
					fv.ProxyKey, fv.ProxyName = p.Key, p.Name
				}
				up += f.Sent
				down += f.Received
				out = append(out, fv)
			}

			s.mu.Lock()
			s.flows = out
			s.stats.Flows = len(out)
			s.stats.Up, s.stats.Down = up, down
			s.mu.Unlock()
		}
	}
}
