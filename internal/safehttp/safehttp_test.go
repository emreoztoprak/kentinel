package safehttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBlocksCloudMetadata is the core SSRF guard: a request to the
// link-local metadata address must fail at dial time.
func TestBlocksCloudMetadata(t *testing.T) {
	client := Client(3 * time.Second)
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/", // AWS/GCP/Azure IPv4 IMDS
		"http://[fe80::1]/",                        // IPv6 link-local
		"http://[fd00:ec2::254]/",                  // AWS IPv6 IMDS
	} {
		_, err := client.Get(target)
		if err == nil {
			t.Errorf("%s: expected the dial to be blocked, got no error", target)
			continue
		}
		if !strings.Contains(err.Error(), "blocked") {
			t.Errorf("%s: expected a block error, got: %v", target, err)
		}
	}
}

// TestAllowsNormalHosts confirms the guard doesn't break legitimate targets
// (the bundled Ollama/Prometheus live on ordinary cluster IPs).
func TestAllowsNormalHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Client(3 * time.Second).Get(srv.URL) // 127.0.0.1 loopback, allowed
	if err != nil {
		t.Fatalf("normal host should be reachable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
