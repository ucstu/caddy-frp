package caddyfrp

import (
	"net"
	"time"

	"github.com/caddyserver/caddy/v2"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/vhost"
)

type HTTPSListener struct{ frpModule }

func (HTTPSListener) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "caddy.listeners.frp", New: func() caddy.Module { return new(HTTPSListener) }}
}

func (wrapper *HTTPSListener) WrapListener(listener net.Listener) net.Listener {
	wrapped := &listenerWrapper{
		InternalListener: netpkg.NewInternalListener(), source: listener, app: wrapper.app,
	}
	go wrapped.accept()
	return wrapped
}

type listenerWrapper struct {
	*netpkg.InternalListener
	source net.Listener
	app    *App
}

func (listener *listenerWrapper) accept() {
	for {
		conn, err := listener.source.Accept()
		if err != nil {
			_ = listener.InternalListener.Close()
			return
		}
		go listener.route(conn)
	}
}

func (listener *listenerWrapper) route(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	replayed, info, err := vhost.GetHTTPSHostname(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return
	}
	target := listener.InternalListener
	if service := listener.app.service.Load(); service != nil {
		if name, found := service.LookupHTTPS(info["Host"]); found {
			options, err := parseProxyOptions(name)
			if err != nil {
				_ = conn.Close()
				return
			}
			if !options.TerminateTLS {
				target = listener.app.https
			}
		}
	}
	if err := target.PutConn(replayed); err != nil {
		_ = conn.Close()
	}
}

func (listener *listenerWrapper) Close() error {
	_ = listener.InternalListener.Close()
	return listener.source.Close()
}

var (
	_ caddy.ListenerWrapper = (*HTTPSListener)(nil)
	_ caddy.Provisioner     = (*HTTPSListener)(nil)
)
