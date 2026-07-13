// Package netutil provides network-related utilities for server-side validation.
package netutil

import (
	"context"
	"fmt"
	"net"
	"net/url"
)

// privateRanges lists CIDR blocks that must not be reachable via user-supplied URLs.
// Includes RFC1918 private ranges, loopback, link-local, cloud metadata, the unspecified
// ("this host") ranges, and multicast.
var privateRanges = mustParsePrivateRanges()

func mustParsePrivateRanges() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"127.0.0.0/8",    // IPv4 loopback
		"169.254.0.0/16", // IPv4 link-local / AWS IMDSv1
		"100.64.0.0/10",  // Carrier-grade NAT (RFC6598)
		"0.0.0.0/8",      // "this host" / unspecified — 0.0.0.0 can route to loopback
		"224.0.0.0/4",    // IPv4 multicast
		"::1/128",        // IPv6 loopback
		"::/128",         // IPv6 unspecified
		"fc00::/7",       // IPv6 unique-local (ULA)
		"fe80::/10",      // IPv6 link-local
		"ff00::/8",       // IPv6 multicast
	}
	result := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("netutil: bad CIDR %q: %v", cidr, err))
		}
		result = append(result, network)
	}
	return result
}

// ValidateRPCURLFormat checks that rawURL is syntactically valid as a CometBFT
// RPC endpoint (correct scheme, non-empty host) without performing DNS
// resolution or private-IP checks. Use only in trusted environments such as
// smoke-test networks where the host is guaranteed not to be user-controlled.
func ValidateRPCURLFormat(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no host")
	}
	return nil
}

// ValidateRPCURL checks that rawURL is safe to use as a CometBFT RPC endpoint.
// It rejects URLs that:
//   - have a scheme other than http or https
//   - have an empty host
//   - resolve to a private, loopback, or link-local IP address
//   - cannot be resolved at all
//
// An empty rawURL is allowed (callers may use "" to mean "no URL set").
func ValidateRPCURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	addrs, err := net.DefaultResolver.LookupHost(context.Background(), host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("host %q resolves to no addresses", host)
	}

	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue // shouldn't happen; LookupHost returns IP strings
		}
		for _, network := range privateRanges {
			if network.Contains(ip) {
				return fmt.Errorf("host %q resolves to a private or reserved address (%s)", host, addr)
			}
		}
	}
	return nil
}
