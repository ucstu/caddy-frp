package caddyfrp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestHTTPSUpstreamAuthority(t *testing.T) {
	for _, host := range []string{"example.com", "EXAMPLE.COM:443", "example.com:20000", "example.com:20001"} {
		request := httptest.NewRequest(http.MethodGet, "https://example.com/path?q=1", nil)
		request.Host = host
		proxy := &httputil.ProxyRequest{In: request, Out: request.Clone(request.Context())}
		rewriteHTTPSRequest(proxy)
		if proxy.Out.URL.Host != "example.com:443" || proxy.Out.Host != host {
			t.Fatalf("host %q: upstream = %q, Host = %q", host, proxy.Out.URL.Host, proxy.Out.Host)
		}
		if proxy.Out.URL.RequestURI() != "/path?q=1" || proxy.Out.Header.Get("X-Forwarded-Host") != host {
			t.Fatalf("request metadata changed: %+v", proxy.Out)
		}
	}
}

func TestProxyOptions(t *testing.T) {
	for _, test := range []struct {
		name    string
		want    proxyOptions
		invalid bool
	}{
		{name: "plain"},
		{name: "web?tls=true", want: proxyOptions{TerminateTLS: true}},
		{name: "web?tls=true&redirect=true", want: proxyOptions{TerminateTLS: true, RedirectHTTPS: true}},
		{name: "web?tls=false&redirect=false"},
		{name: "backend?tls=true&insecure=true", want: proxyOptions{TerminateTLS: true, Insecure: true}},
		{name: "backend?insecure=true", want: proxyOptions{Insecure: true}},
		{name: "backend?insecure=false"},
		{name: "backend?insecure=TRUE", invalid: true},
		{name: "backend?insecure=1", invalid: true},
		{name: "backend?insecure=", invalid: true},
		{name: "backend?insecure=true&insecure=false", invalid: true},
		{name: "web?other=value"},
		{name: "web?tls=TRUE", invalid: true},
		{name: "web?tls=1", invalid: true},
		{name: "web?tls=", invalid: true},
		{name: "web?tls=true&tls=false", invalid: true},
		{name: "web?redirect=true", invalid: true},
		{name: "web?tls=true&redirect=invalid", invalid: true},
		{name: "web?tls=%", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseProxyOptions(test.name)
			if (err != nil) != test.invalid {
				t.Fatalf("parse error = %v, want invalid %v", err, test.invalid)
			}
			if !test.invalid && got != test.want {
				t.Fatalf("options = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestPermissionConfiguration(t *testing.T) {
	for _, module := range []caddyfile.Unmarshaler{
		new(CertificatePermission), new(HTTPHandler), new(HTTPSListener),
	} {
		for _, source := range []string{"frp", "frp example.com", "frp {\n invalid\n}"} {
			err := module.UnmarshalCaddyfile(caddyfile.NewTestDispenser(source))
			if (err == nil) != (source == "frp") {
				t.Fatalf("%T configuration %q: %v", module, source, err)
			}
		}
	}
}

func TestStopWithoutService(t *testing.T) {
	app := new(App)
	for range 2 {
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPermissionWithoutService(t *testing.T) {
	permission := &CertificatePermission{frpModule: frpModule{app: new(App)}}
	if err := permission.CertificateAllowed(context.Background(), "example.com"); err == nil {
		t.Fatal("certificate allowed before frp starts")
	}
}

type routeReaderStub struct {
	https    string
	hasHTTPS bool
	http     []string
}

func (routes routeReaderStub) LookupHTTP(*http.Request) (string, bool) {
	return "", false
}

func (routes routeReaderStub) LookupHTTPS(string) (string, bool) {
	return routes.https, routes.hasHTTPS
}

func (routes routeReaderStub) MatchHTTP(_ string, match func(string) bool) bool {
	for _, name := range routes.http {
		if match(name) {
			return true
		}
	}
	return false
}

func TestReadOnlyCertificatePolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		routes routeReaderStub
		want   bool
	}{
		{"unknown", routeReaderStub{}, false},
		{"HTTP enabled", routeReaderStub{http: []string{"plain", "web?tls=true"}}, true},
		{"HTTP disabled", routeReaderStub{http: []string{"plain"}}, false},
		{"HTTPS enabled", routeReaderStub{https: "tls?tls=true", hasHTTPS: true}, true},
		{"HTTPS overrides HTTP", routeReaderStub{https: "tls", hasHTTPS: true, http: []string{"web?tls=true"}}, false},
		{"empty HTTPS name still matches", routeReaderStub{hasHTTPS: true, http: []string{"web?tls=true"}}, false},
		{"invalid HTTPS overrides HTTP", routeReaderStub{https: "tls?tls=invalid", hasHTTPS: true, http: []string{"web?tls=true"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := allowsHTTPS(test.routes, "example.com"); got != test.want {
				t.Fatalf("allowed = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAccessPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		options   proxyOptions
		clientTLS bool
		want      int
	}{
		{"plain HTTP", proxyOptions{}, false, http.StatusOK},
		{"existing TLS without termination option", proxyOptions{}, true, http.StatusOK},
		{"enabled HTTP", proxyOptions{TerminateTLS: true}, false, http.StatusOK},
		{"terminated HTTP", proxyOptions{TerminateTLS: true}, true, http.StatusOK},
		{"redirect", proxyOptions{TerminateTLS: true, RedirectHTTPS: true}, false, http.StatusPermanentRedirect},
		{"redirect target", proxyOptions{TerminateTLS: true, RedirectHTTPS: true}, true, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.options.status(test.clientTLS); got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}
