// See LICENSE file in the project root for license information.

package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type Policy struct {
	AllowPrivate   bool
	AllowLoopback  bool
	AllowLinkLocal bool
	AllowMulticast bool
	Blocked        []netip.Prefix
	Resolver       Resolver
	Dialer         Dialer
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type ResolvedTarget struct {
	Original string
	Network  string
	Host     string
	Port     int
	Addr     netip.AddrPort
}

var errNoAllowedAddress = errors.New("target has no allowed resolved address")

func DefaultPolicy() Policy {
	return Policy{
		Resolver: net.DefaultResolver,
		Dialer: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}),
		Blocked: []netip.Prefix{
			netip.MustParsePrefix("0.0.0.0/8"),
			netip.MustParsePrefix("100.64.0.0/10"),
			netip.MustParsePrefix("192.0.0.0/24"),
			netip.MustParsePrefix("192.0.2.0/24"),
			netip.MustParsePrefix("198.18.0.0/15"),
			netip.MustParsePrefix("198.51.100.0/24"),
			netip.MustParsePrefix("203.0.113.0/24"),
			netip.MustParsePrefix("224.0.0.0/4"),
			netip.MustParsePrefix("240.0.0.0/4"),
			netip.MustParsePrefix("::/128"),
			netip.MustParsePrefix("::ffff:0:0/96"),
			netip.MustParsePrefix("64:ff9b::/96"),
			netip.MustParsePrefix("2001:db8::/32"),
			netip.MustParsePrefix("ff00::/8"),
		},
	}
}

func (p Policy) ResolveTarget(ctx context.Context, network, raw string) (ResolvedTarget, error) {
	network = strings.TrimSpace(strings.ToLower(network))
	if network != "tcp" && network != "udp" {
		return ResolvedTarget{}, fmt.Errorf("unsupported network %q", network)
	}
	host, port, err := parseAuthority(raw)
	if err != nil {
		return ResolvedTarget{}, err
	}
	resolved, err := p.resolveHost(ctx, host)
	if err != nil {
		return ResolvedTarget{}, err
	}
	for _, addr := range resolved {
		if p.allowAddr(addr) {
			return ResolvedTarget{
				Original: net.JoinHostPort(host, strconv.Itoa(port)),
				Network:  network,
				Host:     host,
				Port:     port,
				Addr:     netip.AddrPortFrom(addr, uint16(port)),
			}, nil
		}
	}
	return ResolvedTarget{}, errNoAllowedAddress
}

func (p Policy) DialTCP(ctx context.Context, raw string) (net.Conn, ResolvedTarget, error) {
	target, err := p.ResolveTarget(ctx, "tcp", raw)
	if err != nil {
		return nil, ResolvedTarget{}, err
	}
	dialer := p.Dialer
	if dialer == nil {
		dialer = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second})
	}
	conn, err := dialer.DialContext(ctx, "tcp", target.Addr.String())
	if err != nil {
		return nil, target, err
	}
	return conn, target, nil
}

func (p Policy) DialUDP(ctx context.Context, raw string) (*net.UDPConn, ResolvedTarget, error) {
	target, err := p.ResolveTarget(ctx, "udp", raw)
	if err != nil {
		return nil, ResolvedTarget{}, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", target.Addr.String())
	if err != nil {
		return nil, target, err
	}
	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		return nil, target, fmt.Errorf("dial udp returned %T", conn)
	}
	return udpConn, target, nil
}

func parseAuthority(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, errors.New("target is empty")
	}
	if strings.ContainsAny(raw, " \t\r\n/") {
		return "", 0, fmt.Errorf("target %q is not an authority", raw)
	}
	host, portText, err := net.SplitHostPort(raw)
	if err != nil {
		return "", 0, fmt.Errorf("target must be host:port: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0, errors.New("target host is empty")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("target port is invalid: %q", portText)
	}
	return host, port, nil
}

func (p Policy) resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if ok {
			out = append(out, addr.Unmap())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("resolve %s: no usable address", host)
	}
	return out, nil
}

func (p Policy) allowAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() && !p.AllowLoopback {
		return false
	}
	if addr.IsPrivate() && !p.AllowPrivate {
		return false
	}
	if addr.IsLinkLocalUnicast() && !p.AllowLinkLocal {
		return false
	}
	if addr.IsMulticast() && !p.AllowMulticast {
		return false
	}
	for _, prefix := range p.Blocked {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
