// Command probe checks the wire protocol against the mock service: handshake,
// requests, events and flows. It is a development tool, not part of the app.
package main

import (
	"fmt"
	"log"
	"time"

	"pbdeck/internal/pbproto"
)

func main() {
	mock := pbproto.NewMock()
	if err := mock.Start(); err != nil {
		log.Fatalf("start mock: %v", err)
	}
	defer mock.Close()

	c, err := pbproto.Dial()
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer c.Close()
	fmt.Println("protocol version:", c.Version())

	id, err := c.AddProxy(pbproto.ProxyConfig{Type: pbproto.ProxySOCKS5, IP: "127.0.0.1", Port: 1080, SendDomain: true})
	fmt.Println("add proxy ->", id, err)

	proc, hosts, ports, domains := "probe.exe", "*", "443", "*.example.com"
	rid, err := c.AddRule(pbproto.RuleSpec{
		Process: &proc, Hosts: &hosts, Ports: &ports, Domains: &domains,
		Protocol: pbproto.ProtoTCP, Action: pbproto.ActionProxy, ProxyID: id,
	})
	fmt.Println("add rule ->", rid, err)
	fmt.Println("rule position ->", must(c.RulePosition(rid)))
	fmt.Println("move rule ->", c.MoveRule(rid, 1))
	if err := c.SetLogAll(true); err != nil {
		fmt.Println("set log all:", err)
	}
	if err := c.TestProxy(id, "example.com", 443); err != nil {
		fmt.Println("test proxy:", err)
	}

	deadline := time.After(4 * time.Second)
	conns, tests := 0, 0
loop:
	for {
		select {
		case ev := <-c.Events():
			switch ev.Kind {
			case pbproto.EvtConn:
				conns++
				if conns <= 3 {
					fmt.Printf("conn: %-14s %-16s:%-5d %s\n", ev.Conn.Process, ev.Conn.IP, ev.Conn.Port, ev.Conn.Info)
				}
			case pbproto.EvtTestLine:
				tests++
				fmt.Println("test:", ev.TestLine)
			case pbproto.EvtTestDone:
				fmt.Println("test done, result:", ev.TestResult)
			case pbproto.EvtLog:
				fmt.Println("log:", ev.Log)
			}
		case <-deadline:
			break loop
		}
	}

	flows, err := c.Flows(400)
	fmt.Println("flows ->", len(flows), err)
	for i, f := range flows {
		if i >= 3 {
			break
		}
		fmt.Printf("  %-36s %-12s pid=%-5d up=%-8d down=%-8d age=%ds\n", f.Host, f.Name, f.PID, f.Sent, f.Received, f.AgeMS/1000)
	}
	fmt.Printf("events: %d conns, %d test lines\n", conns, tests)
}

func must(v uint32, err error) uint32 {
	if err != nil {
		log.Fatalf("rule position: %v", err)
	}
	return v
}
