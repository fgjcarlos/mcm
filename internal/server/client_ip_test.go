package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustTrusted(t *testing.T, entries ...string) []*net.IPNet {
	t.Helper()
	nets, err := parseTrustedProxies(entries)
	if err != nil {
		t.Fatalf("parseTrustedProxies(%v): %v", entries, err)
	}
	return nets
}

func requestWith(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeers(t *testing.T) {
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		trusted []string
		want    string
	}{
		{
			name:    "no trusted proxies ignores spoofed XFF",
			remote:  "203.0.113.50:5000",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4"},
			trusted: nil,
			want:    "203.0.113.50",
		},
		{
			name:    "untrusted peer ignores XFF",
			remote:  "203.0.113.50:5000",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4"},
			trusted: []string{"10.0.0.0/8"},
			want:    "203.0.113.50",
		},
		{
			name:    "trusted peer honors single XFF",
			remote:  "10.0.0.2:5000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			trusted: []string{"10.0.0.0/8"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted peer walks XFF right-to-left skipping trusted hops",
			remote:  "10.0.0.2:5000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.9"},
			trusted: []string{"10.0.0.0/8"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted peer falls back to X-Real-IP when no XFF",
			remote:  "10.0.0.2:5000",
			headers: map[string]string{"X-Real-IP": "198.51.100.9"},
			trusted: []string{"10.0.0.0/8"},
			want:    "198.51.100.9",
		},
		{
			name:    "trusted peer with all-trusted XFF returns leftmost",
			remote:  "10.0.0.2:5000",
			headers: map[string]string{"X-Forwarded-For": "10.0.0.3, 10.0.0.9"},
			trusted: []string{"10.0.0.0/8"},
			want:    "10.0.0.3",
		},
		{
			name:    "no headers uses remote addr",
			remote:  "192.0.2.10:5000",
			headers: nil,
			trusted: []string{"10.0.0.0/8"},
			want:    "192.0.2.10",
		},
		{
			name:    "single trusted host by bare IP",
			remote:  "127.0.0.1:5000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			trusted: []string{"127.0.0.1"},
			want:    "203.0.113.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clientIP(requestWith(tc.remote, tc.headers), mustTrusted(t, tc.trusted...))
			if got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	nets, err := parseTrustedProxies([]string{"10.0.0.0/8", "127.0.0.1", "::1", "  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 3 {
		t.Fatalf("got %d networks, want 3 (blank entry skipped)", len(nets))
	}

	if _, err := parseTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error for invalid entry, got nil")
	}
}
