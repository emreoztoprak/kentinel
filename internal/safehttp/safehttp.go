// Package safehttp builds HTTP clients for reaching user-supplied URLs (the
// Ollama host, Prometheus URL, and notification webhooks — all settable from
// the Settings UI). Its dialer refuses connections to link-local /
// cloud-metadata addresses so those URLs can't be pointed at
// 169.254.169.254 to read a node's cloud IAM credentials from the agent's
// network position.
//
// Cluster-private ranges (10/8, 172.16/12, 192.168/16) are intentionally
// still reachable — the bundled Ollama and Prometheus legitimately live
// there — so this is a targeted block on the metadata endpoints, not a full
// private-network egress filter.
package safehttp

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// awsIMDSv6 is AWS's IPv6 instance-metadata address. It sits in the
// unique-local range (fc00::/7) rather than link-local, so it needs an
// explicit check on top of IsLinkLocalUnicast.
var awsIMDSv6 = net.ParseIP("fd00:ec2::254")

// Client returns an *http.Client with the metadata-blocking dialer and the
// given overall timeout.
func Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   blockMetadata,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// blockMetadata runs after DNS resolution, on the concrete IP about to be
// dialed — so a hostname that resolves to a metadata address (DNS rebinding)
// is caught here, not just literal-IP URLs.
func blockMetadata(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing to dial non-IP address %q", host)
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(awsIMDSv6) {
		return fmt.Errorf("refusing to connect to %s: link-local / cloud-metadata addresses are blocked", ip)
	}
	return nil
}
