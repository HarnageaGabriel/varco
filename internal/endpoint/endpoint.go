// Package endpoint parses numeric network endpoints without DNS.
package endpoint

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Endpoint is a numeric IP address and port.
type Endpoint struct {
	Addr netip.Addr
	Port uint16
}

// Parse accepts an IP, IPv4:port, or [IPv6]:port. It never resolves names.
func Parse(value string, defaultPort uint16) (Endpoint, error) {
	if value == "" {
		return Endpoint{}, fmt.Errorf("endpoint is empty")
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		return Endpoint{Addr: addr.Unmap(), Port: defaultPort}, nil
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return Endpoint{}, fmt.Errorf("parse endpoint %q: %w", value, err)
	}
	if strings.Contains(host, ":") && (!strings.HasPrefix(value, "[") || !strings.Contains(value, "]:")) {
		return Endpoint{}, fmt.Errorf("IPv6 with a port must be bracketed")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return Endpoint{}, fmt.Errorf("endpoint host must be an IP address: %w", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return Endpoint{}, fmt.Errorf("invalid endpoint port %q", portText)
	}
	return Endpoint{Addr: addr.Unmap(), Port: uint16(port)}, nil
}

// String returns the endpoint in dialable host:port form.
func (e Endpoint) String() string {
	return net.JoinHostPort(e.Addr.String(), strconv.FormatUint(uint64(e.Port), 10))
}
