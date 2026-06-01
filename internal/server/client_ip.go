package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// parseTrustedProxies parses IP or CIDR strings into networks. A bare IP is
// treated as a single-host network. Blank entries are skipped.
func parseTrustedProxies(entries []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: not an IP address or CIDR", entry)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return nets, nil
}

// isTrustedProxy reports whether ip falls within any trusted network.
func isTrustedProxy(ip net.IP, trusted []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the best-effort client IP for r.
//
// X-Forwarded-For and X-Real-IP are client-supplied and therefore only trusted
// when the direct peer (RemoteAddr) is a configured trusted proxy. In that case
// the XFF chain is walked right-to-left and the first hop that is NOT a trusted
// proxy is returned (the real client entering the trusted boundary). When no
// proxy is trusted, or the peer is untrusted, the peer address is used so that
// spoofed headers cannot influence rate-limit lockouts or audit trails.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	remote := remoteAddrHost(r.RemoteAddr)

	if isTrustedProxy(net.ParseIP(remote), trusted) {
		if forwarded := forwardedClientIP(r, trusted); forwarded != "" {
			return forwarded
		}
	}
	return remote
}

// forwardedClientIP extracts the real client IP from forwarding headers, given
// that the direct peer is already known to be trusted.
func forwardedClientIP(r *http.Request, trusted []*net.IPNet) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		// Walk right-to-left: hops nearest the server are appended last. The
		// first non-trusted hop is the real client.
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				continue
			}
			if isTrustedProxy(ip, trusted) {
				continue
			}
			return ip.String()
		}
		// Every hop was a trusted proxy; fall back to the leftmost valid IP.
		for _, part := range parts {
			if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
				return ip.String()
			}
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		if ip := net.ParseIP(realIP); ip != nil {
			return ip.String()
		}
	}
	return ""
}

// remoteAddrHost extracts the host portion of a RemoteAddr, tolerating values
// with or without a port.
func remoteAddrHost(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		if parsed := net.ParseIP(host); parsed != nil {
			return parsed.String()
		}
		return host
	}
	if parsed := net.ParseIP(remoteAddr); parsed != nil {
		return parsed.String()
	}
	return remoteAddr
}
