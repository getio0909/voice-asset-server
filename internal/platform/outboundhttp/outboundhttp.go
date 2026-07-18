// Package outboundhttp provides a fail-closed HTTP client for operator-selected
// public HTTPS endpoints. It prevents redirects, ambient proxies, and DNS
// rebinding to local or special-use networks.
package outboundhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxEndpointBytes = 2048

var ErrUnsafeEndpoint = errors.New("unsafe outbound endpoint")

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// ParsePublicHTTPSURL validates the URL shape without resolving it. DNS is
// validated and pinned by the transport immediately before each connection.
func ParsePublicHTTPSURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxEndpointBytes || strings.ContainsAny(value, "\x00\r\n\t") {
		return nil, ErrUnsafeEndpoint
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrUnsafeEndpoint
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" || strings.HasSuffix(hostname, ".") || hostname == "localhost" ||
		!strings.Contains(hostname, ".") || strings.HasSuffix(hostname, ".localhost") ||
		strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") ||
		strings.HasSuffix(hostname, ".lan") {
		return nil, ErrUnsafeEndpoint
	}
	if port := parsed.Port(); port != "" {
		portNumber, portErr := strconv.Atoi(port)
		if portErr != nil || portNumber < 1 || portNumber > 65535 {
			return nil, ErrUnsafeEndpoint
		}
	}
	if address, parseErr := netip.ParseAddr(hostname); parseErr == nil && unsafeAddress(address) {
		return nil, ErrUnsafeEndpoint
	}
	return parsed, nil
}

// NewClient returns the production fail-closed transport. A supplied client is
// cloned for deterministic tests; production callers must pass nil.
func NewClient(client *http.Client) *http.Client {
	if client != nil {
		cloned := *client
		cloned.CheckRedirect = rejectRedirect
		return &cloned
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: PublicDialContext(net.DefaultResolver.LookupNetIP, dialer.DialContext),
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       15 * time.Second,
		CheckRedirect: rejectRedirect,
	}
}

func rejectRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// PublicDialContext resolves every candidate itself, rejects the entire DNS
// response if any address is unsafe, and dials only validated IP literals.
func PublicDialContext(
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, endpoint string) (net.Conn, error) {
		if lookup == nil || dial == nil || (network != "tcp" && network != "tcp4" && network != "tcp6") {
			return nil, errors.New("invalid outbound dial configuration")
		}
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil || host == "" || port == "" {
			return nil, errors.New("invalid outbound address")
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return nil, errors.New("invalid outbound port")
		}

		var addresses []netip.Addr
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = lookup(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("resolve outbound endpoint")
			}
		}

		validated := make([]netip.Addr, 0, len(addresses))
		seen := make(map[netip.Addr]struct{}, len(addresses))
		for _, address := range addresses {
			address = address.Unmap()
			if address.Zone() != "" || unsafeAddress(address) {
				return nil, ErrUnsafeEndpoint
			}
			if _, exists := seen[address]; !exists {
				seen[address] = struct{}{}
				validated = append(validated, address)
			}
		}

		var lastErr error
		for _, address := range validated {
			if (network == "tcp4" && !address.Is4()) || (network == "tcp6" && !address.Is6()) {
				continue
			}
			connection, dialErr := dial(ctx, network, net.JoinHostPort(address.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if lastErr != nil {
			return nil, fmt.Errorf("connect outbound endpoint: %w", lastErr)
		}
		return nil, errors.New("no compatible public endpoint address")
	}
}

func unsafeAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsUnspecified() ||
		address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
