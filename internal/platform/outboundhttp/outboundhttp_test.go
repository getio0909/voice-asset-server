package outboundhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"
)

func TestParsePublicHTTPSURLAcceptsOnlyCredentialFreePublicEndpoints(t *testing.T) {
	accepted, err := ParsePublicHTTPSURL(" https://hooks.example.com:10443/voiceasset/events ")
	if err != nil || accepted.String() != "https://hooks.example.com:10443/voiceasset/events" {
		t.Fatalf("ParsePublicHTTPSURL() = (%v, %v)", accepted, err)
	}

	for _, candidate := range []string{
		"http://hooks.example.com/events",
		"https://user@hooks.example.com/events",
		"https://hooks.example.com/events?token=secret",
		"https://hooks.example.com/events#fragment",
		"https://localhost/events",
		"https://service.internal/events",
		"https://singlelabel/events",
		"https://127.0.0.1/events",
		"https://169.254.169.254/latest/meta-data",
		"https://192.0.2.1/events",
	} {
		if _, err := ParsePublicHTTPSURL(candidate); !errors.Is(err, ErrUnsafeEndpoint) {
			t.Fatalf("ParsePublicHTTPSURL(%q) error = %v", candidate, err)
		}
	}
}

func TestPublicDialContextPinsValidatedAddresses(t *testing.T) {
	var dialed string
	dialContext := PublicDialContext(
		func(_ context.Context, network, host string) ([]netip.Addr, error) {
			if network != "ip" || host != "hooks.example.com" {
				t.Fatalf("lookup = (%q, %q)", network, host)
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(_ context.Context, network, address string) (net.Conn, error) {
			dialed = address
			return nil, errors.New("fixture stop")
		},
	)
	if _, err := dialContext(context.Background(), "tcp", "hooks.example.com:443"); err == nil {
		t.Fatal("dial unexpectedly succeeded")
	}
	if dialed != "93.184.216.34:443" {
		t.Fatalf("dialed = %q", dialed)
	}
}

func TestPublicDialContextRejectsAnyUnsafeDNSAnswer(t *testing.T) {
	dialed := false
	dialContext := PublicDialContext(
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("10.0.0.1"),
			}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, nil
		},
	)
	if _, err := dialContext(context.Background(), "tcp", "hooks.example.com:443"); !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("DialContext() error = %v", err)
	}
	if dialed {
		t.Fatal("unsafe mixed DNS answer was dialed")
	}
}

func TestNewClientDisablesRedirectsProxyAndLegacyTLS(t *testing.T) {
	client := NewClient(nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("ambient proxy is enabled")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS minimum = %#v", transport.TLSClientConfig)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://hooks.example.com", nil)
	if err := client.CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect error = %v", err)
	}

	injected := &http.Client{}
	cloned := NewClient(injected)
	if cloned == injected || cloned.CheckRedirect == nil {
		t.Fatal("injected client was not safely cloned")
	}
}
