// Package ssrf provides an opt-in policy that rejects HTTP requests to
// private, link-local, and cloud-metadata IP ranges before they leave the
// process. It is designed for CI pipelines that run YAML configs from
// partially-trusted sources and want a lightweight defence against accidental
// or malicious Server-Side Request Forgery.
//
// The policy is OFF by default. Callers enable it by constructing a Policy
// and passing it to [NewRoundTripper].
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrBlocked is the sentinel error returned when a request is rejected by the
// SSRF policy. It wraps a more specific message with the blocked host.
var ErrBlocked = errors.New("ssrf: request blocked by policy")

// blockedCIDRs is the default set of IP ranges blocked when DenyPrivate is
// true. It covers RFC 1918, loopback, link-local, and the two most common
// cloud-metadata endpoints (AWS/GCP on 169.254.169.254, Azure on 168.63.129.16).
var blockedCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",    // IPv4 loopback
	"::1/128",        // IPv6 loopback
	"10.0.0.0/8",     // RFC 1918 class A
	"172.16.0.0/12",  // RFC 1918 class B
	"192.168.0.0/16", // RFC 1918 class C
	"169.254.0.0/16", // link-local / cloud metadata (AWS, GCP: 169.254.169.254)
	"168.63.0.0/16",  // Azure metadata (168.63.129.16)
	"fc00::/7",       // IPv6 unique local
	"fe80::/10",      // IPv6 link-local
	"0.0.0.0/8",      // "this" network
	"100.64.0.0/10",  // CGNAT shared address space (RFC 6598)
	"64:ff9b::/96",   // NAT64 well-known prefix (embeds IPv4; not caught by To4())
	"64:ff9b:1::/48", // NAT64 local-use prefix (RFC 8215)
})

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("ssrf: invalid built-in CIDR " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// Policy controls which outbound requests are permitted.
type Policy struct {
	// DenyPrivate blocks requests whose resolved IP falls in a private,
	// link-local, loopback, or cloud-metadata range.
	DenyPrivate bool

	// AllowHosts is an optional allowlist of hostnames (without port) that
	// are always permitted even when DenyPrivate is true. Matching is exact
	// (case-insensitive); wildcards are not supported.
	AllowHosts []string

	// DenyHosts is an optional denylist of hostnames (without port) that are
	// always rejected regardless of DenyPrivate. Evaluated before AllowHosts.
	DenyHosts []string
}

// DefaultDenyPrivatePolicy returns a Policy with DenyPrivate enabled and no
// allow/deny host overrides. Use this as a starting point for CI pipelines
// that run YAML from partially-trusted sources.
func DefaultDenyPrivatePolicy() Policy {
	return Policy{DenyPrivate: true}
}

// ipBlocked reports whether ip falls in a private, loopback, link-local,
// unspecified, or otherwise sensitive range. IPv4-mapped IPv6 addresses
// (e.g. ::ffff:127.0.0.1) are normalized to their IPv4 form first so they
// cannot bypass an IPv4 CIDR match. The standard-library predicates cover the
// common cases; blockedCIDRs adds ranges they miss (cloud metadata, CGNAT,
// NAT64).
func ipBlocked(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// Check evaluates rawURL against the policy. It returns a wrapped ErrBlocked
// when the request is rejected, or nil when it is permitted.
//
// When the host is a name rather than an IP literal, DNS resolution is
// performed and the request is blocked if ANY resolved address falls in a
// blocked range, defeating multi-record bypasses. Note that Check resolves the
// host independently of the eventual dial, so it remains vulnerable to DNS
// rebinding (TOCTOU); for full protection against an attacker-controlled
// resolver, wrap the transport with NewSafeTransport, which validates the IP
// at dial time and pins it for the connection.
func (p *Policy) Check(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: cannot parse URL: %v", ErrBlocked, err)
	}

	host := strings.ToLower(u.Hostname())

	// Denylist is checked first so explicit blocks cannot be bypassed by
	// adding the host to AllowHosts.
	for _, denied := range p.DenyHosts {
		if strings.EqualFold(denied, host) {
			return fmt.Errorf("%w: host %q is on the deny list", ErrBlocked, host)
		}
	}

	// Allowlist bypasses the private-range check.
	for _, allowed := range p.AllowHosts {
		if strings.EqualFold(allowed, host) {
			return nil
		}
	}

	if !p.DenyPrivate {
		return nil
	}

	// If the host is an IP literal, check it directly.
	if ip := net.ParseIP(host); ip != nil {
		if ipBlocked(ip) {
			return fmt.Errorf("%w: host %q is in a blocked range", ErrBlocked, host)
		}
		return nil
	}

	// Otherwise resolve the host and block if ANY resolved address is in a
	// blocked range (a single public record must not unblock a host that also
	// resolves to a private one).
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		// Cannot resolve → block conservatively, but do not wrap ErrBlocked
		// so callers can distinguish a DNS failure from an explicit policy
		// rejection (errors.Is(err, ErrBlocked) returns false here).
		return fmt.Errorf("ssrf: cannot resolve host %q: %w", host, err)
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			return fmt.Errorf("%w: cannot parse resolved IP %q for host %q", ErrBlocked, a, host)
		}
		if ipBlocked(ip) {
			return fmt.Errorf("%w: host %q resolves to %s which is in a blocked range",
				ErrBlocked, host, ip)
		}
	}

	return nil
}

// roundTripper wraps an http.RoundTripper with an SSRF policy check.
type roundTripper struct {
	policy Policy
	inner  http.RoundTripper
}

// NewRoundTripper returns an http.RoundTripper that runs the SSRF policy
// check before delegating to inner. Use it to wrap the http.Client.Transport
// in transport.HTTPClientConfig when building scenarios with SSRF protection.
//
//	rt := ssrf.NewRoundTripper(ssrf.DefaultDenyPrivatePolicy(), http.DefaultTransport)
//	client := transport.NewHTTPClientWith(transport.HTTPClientConfig{Transport: rt})
func NewRoundTripper(policy Policy, inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &roundTripper{policy: policy, inner: inner}
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := rt.policy.Check(req.URL.String()); err != nil {
		return nil, err
	}
	return rt.inner.RoundTrip(req)
}

// DialContext returns a dial function that resolves the target host, blocks the
// connection if any resolved address is in a blocked range, and then dials the
// validated address directly (pinning the IP). Pinning closes the DNS-rebinding
// TOCTOU window that Check alone cannot: the address that is validated is the
// exact address that is connected to, with no second resolution in between.
//
// When DenyPrivate is false the dialer is a passthrough.
func (p *Policy) DialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		base = &net.Dialer{Timeout: 30 * time.Second}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !p.DenyPrivate {
			return base.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("ssrf: cannot resolve host %q: %w", host, err)
		}
		for _, ipa := range ips {
			if ipBlocked(ipa.IP) {
				return nil, fmt.Errorf("%w: host %q resolves to %s which is in a blocked range",
					ErrBlocked, host, ipa.IP)
			}
		}
		// Dial the first validated address directly so the resolved IP cannot
		// change between validation and connection.
		return base.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// NewSafeTransport returns an *http.Transport derived from base (or a clone of
// http.DefaultTransport when base is nil) whose DialContext enforces the policy
// at dial time, pinning the validated IP for the connection. Because each
// redirect hop is dialed afresh, redirects to blocked targets are also rejected.
//
// Prefer this over NewRoundTripper when the caller does not control the
// resolver and DNS rebinding is part of the threat model (e.g. distributed
// workers accepting targets from the network).
func NewSafeTransport(policy Policy, base *http.Transport) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		base = base.Clone()
	}
	base.DialContext = policy.DialContext(nil)
	return base
}
