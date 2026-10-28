package caddyfrp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
	"github.com/fatedier/frp/server"
)

type proxyOptions struct {
	TerminateTLS  bool
	RedirectHTTPS bool
	Insecure      bool
}

func parseProxyOptions(name string) (proxyOptions, error) {
	var options proxyOptions
	_, query, _ := strings.Cut(name, "?")
	values, err := url.ParseQuery(query)
	if err != nil {
		return options, fmt.Errorf("invalid proxy options in %q: %w", name, err)
	}
	for key, target := range map[string]*bool{
		"tls":      &options.TerminateTLS,
		"redirect": &options.RedirectHTTPS,
		"insecure": &options.Insecure,
	} {
		if entries, exists := values[key]; exists {
			if len(entries) != 1 || (entries[0] != "true" && entries[0] != "false") {
				return options, fmt.Errorf("%s in proxy %q requires one true or false value", key, name)
			}
			*target = entries[0] == "true"
		}
	}
	if options.RedirectHTTPS && !options.TerminateTLS {
		return options, fmt.Errorf("redirect requires tls=true in proxy %q", name)
	}
	return options, nil
}

func (options proxyOptions) status(clientTLS bool) int {
	if !clientTLS && options.RedirectHTTPS {
		return http.StatusPermanentRedirect
	}
	return http.StatusOK
}

type CertificatePermission struct{ frpModule }

func (CertificatePermission) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "tls.permission.frp", New: func() caddy.Module { return new(CertificatePermission) }}
}

func (permission *CertificatePermission) CertificateAllowed(_ context.Context, name string) error {
	if permission.allowsHTTPS(name) {
		return nil
	}
	return fmt.Errorf("certificate not authorized for %q", name)
}

func (permission *CertificatePermission) allowsHTTPS(host string) bool {
	service := permission.app.service.Load()
	if service == nil {
		return false
	}
	return allowsHTTPS(service, host)
}

func allowsHTTPS(routes server.RouteReader, host string) bool {
	allowed := func(name string) bool {
		options, err := parseProxyOptions(name)
		return err == nil && options.TerminateTLS
	}
	if name, found := routes.LookupHTTPS(host); found {
		return allowed(name)
	}
	return routes.MatchHTTP(host, allowed)
}

var (
	_ caddytls.OnDemandPermission = (*CertificatePermission)(nil)
	_ caddy.Provisioner           = (*CertificatePermission)(nil)
)
