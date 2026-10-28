package caddyfrp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

type HTTPHandler struct {
	frpModule
	httpsProxy         *httputil.ReverseProxy
	insecureHTTPSProxy *httputil.ReverseProxy
}

func (HTTPHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.handlers.frp", New: func() caddy.Module { return new(HTTPHandler) }}
}

func (handler *HTTPHandler) Provision(ctx caddy.Context) error {
	if err := handler.frpModule.Provision(ctx); err != nil {
		return err
	}
	handler.httpsProxy = &httputil.ReverseProxy{
		Rewrite: rewriteHTTPSRequest,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				local, remote := net.Pipe()
				if err := handler.app.https.PutConn(remote); err != nil {
					_ = local.Close()
					_ = remote.Close()
					return nil, err
				}
				return local, nil
			},
		},
	}
	transport := handler.httpsProxy.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	handler.insecureHTTPSProxy = &httputil.ReverseProxy{
		Rewrite:   rewriteHTTPSRequest,
		Transport: transport,
	}
	return nil
}

func rewriteHTTPSRequest(request *httputil.ProxyRequest) {
	host := request.In.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	request.Out.URL.Scheme = "https"
	request.Out.URL.Host = net.JoinHostPort(strings.ToLower(host), "443")
	request.SetXForwarded()
}

func (handler *HTTPHandler) Cleanup() error {
	for _, proxy := range []*httputil.ReverseProxy{handler.httpsProxy, handler.insecureHTTPSProxy} {
		if proxy != nil {
			proxy.Transport.(*http.Transport).CloseIdleConnections()
		}
	}
	return nil
}

func (handler *HTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request, next caddyhttp.Handler) error {
	service := handler.app.service.Load()
	if service == nil {
		return caddyhttp.Error(http.StatusServiceUnavailable, nil)
	}
	host := request.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.ToLower(host)
	name, found := service.LookupHTTPS(host)
	backendTLS := found
	if !found {
		name, found = service.LookupHTTP(request)
	}
	if !found {
		return next.ServeHTTP(writer, request)
	}
	options, err := parseProxyOptions(name)
	if err != nil {
		http.Error(writer, "invalid frp proxy configuration: "+err.Error(), http.StatusBadGateway)
		return nil
	}
	if options.status(request.TLS != nil) == http.StatusPermanentRedirect {
		authority := host
		if port := handler.app.config.VhostHTTPSPort; port != 443 || strings.Contains(host, ":") {
			authority = net.JoinHostPort(host, strconv.Itoa(port))
		}
		http.Redirect(writer, request, "https://"+authority+request.URL.RequestURI(), http.StatusPermanentRedirect)
		return nil
	}
	if backendTLS {
		proxy := handler.httpsProxy
		if options.Insecure {
			proxy = handler.insecureHTTPSProxy
		}
		proxy.ServeHTTP(writer, request)
	} else {
		service.HTTPHandler().ServeHTTP(writer, request)
	}
	return nil
}

var (
	_ caddy.Provisioner           = (*HTTPHandler)(nil)
	_ caddy.CleanerUpper          = (*HTTPHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*HTTPHandler)(nil)
)
