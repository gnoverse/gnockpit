package node

import "net"

// IsPublicIP reports whether s is a valid, routable public IP address. Private
// (RFC1918 / IPv6 ULA), loopback, link-local, multicast, and unspecified
// (0.0.0.0) addresses are rejected — they must never be displayed, geolocated,
// or ASN-looked-up (a private IP leaks internal topology and has no geo value).
func IsPublicIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	// IsGlobalUnicast excludes loopback, link-local, multicast, and unspecified,
	// but is still true for RFC1918 / IPv6 ULA, which IsPrivate then rejects.
	return ip.IsGlobalUnicast() && !ip.IsPrivate()
}

// firstPublicIP returns the first valid public IP among a peer's observed remote
// IP and its advertised external-address host (already port-stripped by
// GetNetInfo), or "" when neither is public.
func firstPublicIP(remoteIP, externalHost string) string {
	if IsPublicIP(remoteIP) {
		return remoteIP
	}
	if IsPublicIP(externalHost) {
		return externalHost
	}
	return ""
}
