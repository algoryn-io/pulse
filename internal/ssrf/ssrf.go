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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ErrBlocked is the sentinel error returned when a request is rejected by the
// SSRF policy. It wraps a more specific message with the blocked host.
var ErrBlocked = errors.New("ssrf: request blocked by policy")

// blockedCIDRs is the default set of IP ranges blocked when DenyPrivate is
// true. It covers RFC 1918, loopback, link-local, and the two most common
// cloud-metadata endpoints (AWS/GCP on 169.254.169.254, Azure on 168.63.129.16).
var blockedCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",     // IPv4 loopback
	"::1/128",         // IPv6 loopback
	"10.0.0.0/8",      // RFC 1918 class A
	"172.16.0.0/12",   // RFC 1918 class B
	"192.168.0.0/16",  // RFC 1918 class C
	"169.254.0.0/16",  // link-local / cloud metadata (AWS, GCP: 169.254.169.254)
	"168.63.0.0/16",   // Azure metadata (168.63.129.16)
	"fc00::/7",        // IPv6 unique local
	"fe80::/10",       // IPv6 link-local
	"0.0.0.0/8",       // "this" network
	"100.64.0.0/10",   // CGNAT shared address space (RFC 6598)
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

// Check evaluates rawURL against the policy. It returns a wrapped ErrBlocked
// when the request is rejected, or nil when it is permitted.
//
// DNS resolution is performed to obtain the target IP; the first resolved
// address is checked. For production hardening consider resolving all
// addresses and blocking if any is private.
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

	// Resolve the host to an IP. If it is already an IP literal, ParseIP
	// handles it directly.
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupHost(host)
		if err != nil || len(addrs) == 0 {
			// Cannot resolve → block conservatively.
			return fmt.Errorf("%w: cannot resolve host %q", ErrBlocked, host)
		}
		ip = net.ParseIP(addrs[0])
	}

	if ip == nil {
		return fmt.Errorf("%w: cannot parse resolved IP for host %q", ErrBlocked, host)
	}

	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return fmt.Errorf("%w: host %q resolves to %s which is in blocked range %s",
				ErrBlocked, host, ip, cidr)
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
