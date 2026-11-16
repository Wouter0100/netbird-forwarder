package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/pires/go-proxyproto"
	"gopkg.in/yaml.v3"
)

// routeDialTimeout bounds the dial to a route target. The client's connection
// waits in the NetBird forwarder until it returns.
const routeDialTimeout = 5 * time.Second

var (
	errNoRoute        = errors.New("no route for this address")
	errUnsupportedNet = errors.New("only tcp is routed")
)

type routeFile struct {
	Routes []routeEntry `yaml:"routes"`
}

type routeEntry struct {
	VIP           string `yaml:"vip"`
	Port          uint16 `yaml:"port"`
	Target        string `yaml:"target"`
	ProxyProtocol bool   `yaml:"proxyProtocol"`
}

type route struct {
	target        string
	proxyProtocol bool
}

// routeTable maps a virtual address and port that peers dial to the backend
// that serves it. Only listed addresses are served: anything else that reaches
// this routing peer is refused, whatever the NetBird policies allow.
type routeTable map[netip.AddrPort]route

func loadRoutes(path string) (routeTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseRoutes(data)
}

func parseRoutes(data []byte) (routeTable, error) {
	var file routeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse routes: %w", err)
	}
	if len(file.Routes) == 0 {
		return nil, errors.New("parse routes: no routes")
	}

	table := make(routeTable, len(file.Routes))
	for i, e := range file.Routes {
		vip, err := netip.ParseAddr(e.VIP)
		if err != nil {
			return nil, fmt.Errorf("route %d: vip %q: %w", i, e.VIP, err)
		}
		if e.Port == 0 {
			return nil, fmt.Errorf("route %d: port is required", i)
		}
		if _, _, err := net.SplitHostPort(e.Target); err != nil {
			return nil, fmt.Errorf("route %d: target %q: %w", i, e.Target, err)
		}

		key := netip.AddrPortFrom(vip.Unmap(), e.Port)
		if _, dup := table[key]; dup {
			return nil, fmt.Errorf("route %d: %s is listed twice", i, key)
		}
		table[key] = route{target: e.Target, proxyProtocol: e.ProxyProtocol}
	}
	return table, nil
}

// dial is the NetBird routed dialer: it opens the backend connection for a flow
// that a peer opened to dst.
func (t routeTable) dial(ctx context.Context, network string, src, dst netip.AddrPort) (net.Conn, error) {
	if network != "tcp" {
		return nil, errUnsupportedNet
	}

	dst = netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
	r, ok := t[dst]
	if !ok {
		log.Printf("Refused routed connection from %s to %s: %v", src, dst, errNoRoute)
		return nil, errNoRoute
	}

	log.Printf("Routed connection from %s to %s, dialing target %s", src, dst, r.target)

	conn, err := (&net.Dialer{Timeout: routeDialTimeout}).DialContext(ctx, "tcp", r.target)
	if err != nil {
		log.Printf("Failed to dial target %s for %s: %v", r.target, dst, err)
		return nil, err
	}

	if r.proxyProtocol {
		if err := writeProxyHeader(conn, src, dst); err != nil {
			_ = conn.Close()
			log.Printf("Failed to write PROXY protocol header to target %s: %v", r.target, err)
			return nil, err
		}
	}
	return conn, nil
}

// writeProxyHeader tells the target which peer opened the connection and which
// virtual address it dialled.
func writeProxyHeader(conn net.Conn, src, dst netip.AddrPort) error {
	header := &proxyproto.Header{
		Version:           1,
		Command:           proxyproto.PROXY,
		TransportProtocol: proxyproto.TCPv4,
		SourceAddr:        net.TCPAddrFromAddrPort(netip.AddrPortFrom(src.Addr().Unmap(), src.Port())),
		DestinationAddr:   net.TCPAddrFromAddrPort(dst),
	}
	if src.Addr().Unmap().Is6() {
		header.TransportProtocol = proxyproto.TCPv6
	}
	_, err := header.WriteTo(conn)
	return err
}
