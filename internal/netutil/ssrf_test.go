package netutil_test

import (
	"testing"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/netutil"
)

func TestValidateRPCURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Empty URL is allowed (means "no URL set").
		{name: "empty allowed", url: "", wantErr: false},

		// Scheme validation.
		{name: "ftp scheme rejected", url: "ftp://example.com/rpc", wantErr: true},
		{name: "ws scheme rejected", url: "ws://example.com/rpc", wantErr: true},
		{name: "no scheme rejected", url: "example.com:26657", wantErr: true},

		// RFC1918 private ranges — all rejected.
		{name: "10.x.x.x rejected", url: "http://10.0.0.1:26657", wantErr: true},
		{name: "172.16.x.x rejected", url: "http://172.16.0.1:26657", wantErr: true},
		{name: "192.168.x.x rejected", url: "http://192.168.1.100:26657", wantErr: true},

		// Loopback — rejected.
		{name: "IPv4 loopback rejected", url: "http://127.0.0.1:26657", wantErr: true},
		{name: "IPv4 loopback 127.x.x.x rejected", url: "http://127.1.2.3:26657", wantErr: true},
		{name: "IPv6 loopback rejected", url: "http://[::1]:26657", wantErr: true},

		// Link-local — rejected.
		{name: "IPv4 link-local (APIPA) rejected", url: "http://169.254.169.254", wantErr: true},
		{name: "IPv4 link-local AWS metadata rejected", url: "http://169.254.169.254/latest/meta-data/", wantErr: true},

		// Carrier-grade NAT — rejected.
		{name: "CGNAT 100.64.x.x rejected", url: "http://100.64.0.1:26657", wantErr: true},

		// localhost hostname resolves to loopback — rejected.
		{name: "localhost hostname rejected", url: "http://localhost:26657", wantErr: true},

		// Unresolvable hostname — rejected.
		{name: "unresolvable hostname rejected", url: "http://this-host-does-not-exist.invalid:26657", wantErr: true},

		// Clearly public IPs — accepted.
		// 203.0.113.0/24 is TEST-NET-3 (documentation range, not routable but not
		// in any private CIDR we block). LookupHost returns the IP directly for
		// numeric hosts, so no actual DNS is needed.
		{name: "public IPv4 accepted", url: "http://203.0.113.1:26657", wantErr: false},
		{name: "public IPv4 with https accepted", url: "https://203.0.113.1:26657", wantErr: false},
		{name: "public IPv4 with path accepted", url: "http://203.0.113.1:26657/rpc", wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := netutil.ValidateRPCURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateRPCURL(%q) error = %v, wantErr %v", tc.url, err, tc.wantErr)
			}
		})
	}
}
