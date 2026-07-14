package server

import (
	"net/http"
	"testing"
)

func TestCheckExecOrigin(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin header (native client)", "localhost:8080", "", true},
		{"same-origin production/docker", "localhost:8080", "http://localhost:8080", true},
		{"vite dev proxy, different port", "localhost:8080", "http://localhost:5173", true},
		{"host without port", "kentinel.example.com", "https://kentinel.example.com", true},
		{"cross-site attacker page", "localhost:8080", "https://evil.example", false},
		{"cross-site attacker page targeting a real host", "kentinel.example.com", "https://evil.example", false},
		{"unparsable origin", "localhost:8080", "://not a url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{Host: tc.host, Header: http.Header{}}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := checkExecOrigin(req); got != tc.want {
				t.Errorf("checkExecOrigin(host=%q, origin=%q) = %v, want %v", tc.host, tc.origin, got, tc.want)
			}
		})
	}
}
