package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	table, err := parseRoutes([]byte(`
routes:
  - vip: 10.250.0.10
    port: 5432
    target: postgres.db.svc.cluster.local:5432
  - vip: 10.250.0.11
    port: 443
    target: grafana.monitoring.svc.cluster.local:3000
    proxyProtocol: true
`))
	if err != nil {
		t.Fatalf("parseRoutes() error = %v", err)
	}

	want := routeTable{
		netip.MustParseAddrPort("10.250.0.10:5432"): {target: "postgres.db.svc.cluster.local:5432"},
		netip.MustParseAddrPort("10.250.0.11:443"):  {target: "grafana.monitoring.svc.cluster.local:3000", proxyProtocol: true},
	}
	if len(table) != len(want) {
		t.Fatalf("parseRoutes() = %v, want %v", table, want)
	}
	for k, v := range want {
		if table[k] != v {
			t.Fatalf("route %s = %+v, want %+v", k, table[k], v)
		}
	}
}

func TestParseRoutesRejects(t *testing.T) {
	tests := map[string]string{
		"empty":           `routes: []`,
		"bad vip":         "routes:\n  - {vip: nope, port: 80, target: a:80}",
		"missing port":    "routes:\n  - {vip: 10.250.0.1, target: a:80}",
		"portless target": "routes:\n  - {vip: 10.250.0.1, port: 80, target: a}",
		"duplicate": "routes:\n  - {vip: 10.250.0.1, port: 80, target: a:80}\n" +
			"  - {vip: 10.250.0.1, port: 80, target: b:80}",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRoutes([]byte(in)); err == nil {
				t.Fatal("parseRoutes() error = nil, want an error")
			}
		})
	}
}

func TestRouteTableDialRefusesUnlisted(t *testing.T) {
	table := routeTable{netip.MustParseAddrPort("10.250.0.10:80"): {target: "127.0.0.1:1"}}
	src := netip.MustParseAddrPort("100.64.0.7:40000")

	if _, err := table.dial(context.Background(), "tcp", src, netip.MustParseAddrPort("10.250.0.10:81")); !errors.Is(err, errNoRoute) {
		t.Fatalf("dial(unlisted port) error = %v, want errNoRoute", err)
	}
	if _, err := table.dial(context.Background(), "tcp", src, netip.MustParseAddrPort("10.250.0.99:80")); !errors.Is(err, errNoRoute) {
		t.Fatalf("dial(unlisted vip) error = %v, want errNoRoute", err)
	}
	if _, err := table.dial(context.Background(), "udp", src, netip.MustParseAddrPort("10.250.0.10:80")); !errors.Is(err, errUnsupportedNet) {
		t.Fatalf("dial(udp) error = %v, want errUnsupportedNet", err)
	}
}

func TestRouteTableDialWritesProxyHeader(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			got <- err.Error()
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		got <- line
	}()

	vip := netip.MustParseAddrPort("10.250.0.10:5432")
	table := routeTable{vip: {target: l.Addr().String(), proxyProtocol: true}}

	conn, err := table.dial(context.Background(), "tcp", netip.MustParseAddrPort("100.64.0.7:40000"), vip)
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	defer conn.Close()

	if line := <-got; !strings.HasPrefix(line, "PROXY TCP4 100.64.0.7 10.250.0.10 40000 5432") {
		t.Fatalf("PROXY header = %q", line)
	}
}
