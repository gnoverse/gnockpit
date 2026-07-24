package node

import "testing"

func TestIsPublicIP(t *testing.T) {
	public := []string{
		"1.2.3.4", "8.8.8.8", "54.145.44.95", "2606:4700:4700::1111",
	}
	notPublic := []string{
		"10.0.0.1", "172.16.5.5", "172.31.255.255", "192.168.1.1", // RFC1918
		"127.0.0.1", "169.254.1.1", "0.0.0.0", // loopback, link-local, unspecified
		"::1", "fc00::1", "fd12::34", "fe80::1", // IPv6 loopback/ULA/link-local
		"255.255.255.255", "224.0.0.1", // broadcast, multicast
		"not-an-ip", "", "1.2.3.4:26657", // malformed (with port -> not a bare IP)
	}
	for _, s := range public {
		if !IsPublicIP(s) {
			t.Errorf("IsPublicIP(%q) = false, want true", s)
		}
	}
	for _, s := range notPublic {
		if IsPublicIP(s) {
			t.Errorf("IsPublicIP(%q) = true, want false", s)
		}
	}
}

func TestFirstPublicIP(t *testing.T) {
	// remote IP private, external host public -> external wins.
	if got := firstPublicIP("10.0.0.5", "203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("firstPublicIP = %q, want 203.0.113.7", got)
	}
	// remote IP public -> remote wins (checked first).
	if got := firstPublicIP("203.0.113.7", "198.51.100.9"); got != "203.0.113.7" {
		t.Errorf("firstPublicIP = %q, want 203.0.113.7", got)
	}
	// both private/invalid -> empty.
	if got := firstPublicIP("10.0.0.5", "192.168.0.1"); got != "" {
		t.Errorf("firstPublicIP = %q, want empty", got)
	}
}
