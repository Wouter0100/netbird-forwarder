package main

import (
	"net"
	"testing"
)

// The self-probe is recognised by comparing the connection's source with the
// listener's own address, so a wrong answer here either sends probe traffic to
// the target or hands a client's connection back unserved.
func TestAddrHost(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{"netbird tcp address", &net.TCPAddr{IP: net.ParseIP("100.64.0.7"), Port: 3306}, "100.64.0.7"},
		{"ipv6 tcp address", &net.TCPAddr{IP: net.ParseIP("fd00::1"), Port: 3306}, "fd00::1"},
		{"host:port string", stringAddr("100.64.0.7:51820"), "100.64.0.7"},
		{"portless string", stringAddr("100.64.0.7"), ""},
		{"nil", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := addrHost(tt.addr); got != tt.want {
				t.Fatalf("addrHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

type stringAddr string

func (stringAddr) Network() string  { return "tcp" }
func (a stringAddr) String() string { return string(a) }
