package controller

import (
	"net"
	"testing"
)

func TestValidateControllerRuntimeAuthorityPolicy(t *testing.T) {
	const token = "controller-token-0123456789abcdef0123456789"
	_, trustedProxy, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		host  string
		port  int
		auth  AuthConfig
		valid bool
	}{
		{name: "loopback default", host: "127.0.0.1", port: DefaultControllerPort, valid: true},
		{name: "localhost", host: "localhost", port: DefaultControllerPort, valid: true},
		{name: "remote missing public origin", host: "0.0.0.0", port: DefaultControllerPort},
		{name: "remote missing auth policy", host: "::", port: DefaultControllerPort, auth: AuthConfig{PublicOrigin: "http://pixie.example"}},
		{name: "remote authenticated", host: "0.0.0.0", port: DefaultControllerPort, auth: AuthConfig{Enabled: true, ControllerToken: token, MCPToken: "mcp-token-0123456789abcdef0123456789", PublicOrigin: "https://pixie.example"}, valid: true},
		{name: "remote explicit no-auth policy", host: "::", port: DefaultControllerPort, auth: AuthConfig{MCPToken: "mcp-token-0123456789abcdef0123456789", PublicOrigin: "http://pixie.example", AllowRemoteWithout: true}, valid: true},
		{name: "trusted proxy without controller auth", host: "0.0.0.0", port: DefaultControllerPort, auth: AuthConfig{PublicOrigin: "https://pixie.example", AllowRemoteWithout: true, TrustedProxyCIDRs: []net.IPNet{*trustedProxy}}},
		{name: "trusted proxy with controller auth", host: "0.0.0.0", port: DefaultControllerPort, auth: AuthConfig{Enabled: true, ControllerToken: token, MCPToken: "mcp-token-0123456789abcdef0123456789", PublicOrigin: "https://pixie.example", TrustedProxyCIDRs: []net.IPNet{*trustedProxy}}, valid: true},
		{name: "unsupported host", host: "remote.example", port: DefaultControllerPort},
		{name: "zero port", host: "127.0.0.1", port: 0},
		{name: "too-large port", host: "127.0.0.1", port: 65536},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateControllerRuntime(test.host, test.port, test.auth)
			if (err == nil) != test.valid {
				t.Fatalf("validateControllerRuntime(%q, %d, %#v) = %v, valid=%v", test.host, test.port, test.auth, err, test.valid)
			}
		})
	}
}
