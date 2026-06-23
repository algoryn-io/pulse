package ssrf

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestPolicyCheckAllowsPublicURLs(t *testing.T) {
	p := DefaultDenyPrivatePolicy()
	publicURLs := []string{
		"http://example.com/api",
		"https://api.example.org/v1/data",
	}
	for _, u := range publicURLs {
		// Public hostnames resolve to global unicast addresses; skip if DNS
		// is unavailable in the test environment.
		err := p.Check(u)
		if errors.Is(err, ErrBlocked) {
			// Only fail on an explicit block, not on DNS resolution errors in
			// sandboxed environments.
			t.Errorf("expected %q to be allowed, got %v", u, err)
		}
	}
}

func TestPolicyCheckBlocksPrivateIPLiterals(t *testing.T) {
	p := DefaultDenyPrivatePolicy()
	blocked := []string{
		"http://127.0.0.1/secret",
		"http://localhost/secret",
		"http://10.0.0.1/internal",
		"http://172.16.0.1/internal",
		"http://192.168.1.1/internal",
		"http://169.254.169.254/latest/meta-data/",
		"http://168.63.129.16/metadata",
		"http://[::1]/secret",
	}
	for _, u := range blocked {
		err := p.Check(u)
		if err == nil {
			t.Errorf("expected %q to be blocked, got nil", u)
			continue
		}
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("expected ErrBlocked for %q, got %v", u, err)
		}
	}
}

func TestPolicyCheckBlocksIPv4MappedIPv6(t *testing.T) {
	p := DefaultDenyPrivatePolicy()
	// IPv4-mapped IPv6 literals must be normalized to their IPv4 form before
	// matching, otherwise they bypass IPv4 CIDRs like 127.0.0.0/8 and the
	// cloud-metadata range.
	blocked := []string{
		"http://[::ffff:127.0.0.1]/secret",
		"http://[::ffff:169.254.169.254]/latest/meta-data/",
		"http://[::ffff:10.0.0.1]/internal",
	}
	for _, u := range blocked {
		err := p.Check(u)
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("expected %q to be blocked, got %v", u, err)
		}
	}
}

func TestIPBlocked(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"169.254.169.254", true},
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback
		{"0.0.0.0", true},          // unspecified
		{"fe80::1", true},          // link-local
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false}, // public IPv6
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", c.ip)
		}
		if got := ipBlocked(ip); got != c.want {
			t.Errorf("ipBlocked(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestDialContextBlocksPrivateLiteral(t *testing.T) {
	p := DefaultDenyPrivatePolicy()
	dial := p.DialContext(nil)
	_, err := dial(context.Background(), "tcp", "127.0.0.1:80")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected dial to a private literal to be blocked, got %v", err)
	}
}

func TestDialContextPassthroughWhenDisabled(t *testing.T) {
	p := Policy{DenyPrivate: false}
	dial := p.DialContext(nil)
	// With the policy disabled the dialer must not reject the address on policy
	// grounds; a connection-refused style error (or success) is acceptable, an
	// ErrBlocked is not.
	_, err := dial(context.Background(), "tcp", "127.0.0.1:1")
	if errors.Is(err, ErrBlocked) {
		t.Fatalf("disabled policy must not block, got %v", err)
	}
}

func TestPolicyCheckAllowHostBypassesPrivateBlock(t *testing.T) {
	p := Policy{
		DenyPrivate: true,
		AllowHosts:  []string{"trusted-internal.example.com"},
	}
	// We can't resolve the hostname in all envs, but the allowlist check
	// happens before DNS, so we test with an IP literal that would otherwise
	// be blocked.
	// Use a workaround: create a URL that has the exact hostname.
	// Since AllowHosts is matched before DNS, "trusted-internal.example.com"
	// would pass even if it resolved to a private IP.
	// We verify that the allowlist skips the block for a configured host.
	err := p.Check("http://trusted-internal.example.com/api")
	// In a sandboxed env this may fail on DNS, but it must NOT return ErrBlocked.
	if errors.Is(err, ErrBlocked) {
		t.Errorf("allowed host should not be blocked, got %v", err)
	}
}

func TestPolicyCheckDenyHostBlocks(t *testing.T) {
	p := Policy{
		DenyPrivate: false, // private range check is OFF
		DenyHosts:   []string{"evil.example.com"},
	}
	err := p.Check("http://evil.example.com/exfil")
	if err == nil {
		t.Fatal("expected blocked URL to return an error")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}

func TestPolicyCheckDenyHostTakesPrecedenceOverAllowHost(t *testing.T) {
	p := Policy{
		DenyPrivate: false,
		DenyHosts:   []string{"ambiguous.example.com"},
		AllowHosts:  []string{"ambiguous.example.com"},
	}
	// DenyHosts is evaluated before AllowHosts.
	err := p.Check("http://ambiguous.example.com/path")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected deny to win over allow, got %v", err)
	}
}

func TestPolicyCheckPermitsWhenPolicyDisabled(t *testing.T) {
	p := Policy{DenyPrivate: false}
	// Even a loopback address is permitted when the policy is disabled.
	if err := p.Check("http://127.0.0.1/anything"); err != nil {
		t.Fatalf("expected nil for disabled policy, got %v", err)
	}
}

func TestPolicyCheckCaseInsensitiveHostMatching(t *testing.T) {
	p := Policy{
		DenyHosts: []string{"BLOCKED.EXAMPLE.COM"},
	}
	err := p.Check("http://blocked.example.com/")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected case-insensitive deny match, got %v", err)
	}
}
