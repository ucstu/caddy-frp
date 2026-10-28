package caddyfrp

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/fatedier/frp/pkg/config"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/util/log"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/server"
)

func init() {
	caddy.RegisterModule(&App{})
	caddy.RegisterModule(HTTPHandler{})
	caddy.RegisterModule(HTTPSListener{})
	caddy.RegisterModule(CertificatePermission{})
	httpcaddyfile.RegisterGlobalOption("frps", parseGlobal)
	httpcaddyfile.RegisterHandlerDirective("frp", func(helper httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
		handler := new(HTTPHandler)
		return handler, handler.UnmarshalCaddyfile(helper.Dispenser)
	})
}

type App struct {
	ConfigFile string `json:"config_file"`
	config     *v1.ServerConfig
	context    caddy.Context
	service    atomic.Pointer[server.Service]
	https      *netpkg.InternalListener
}

func (*App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "frps", New: func() caddy.Module { return new(App) }}
}

func (app *App) Provision(ctx caddy.Context) error {
	if app.ConfigFile == "" {
		return fmt.Errorf("frps config_file is required")
	}
	loaded, _, err := config.LoadServerConfig(app.ConfigFile, true)
	if err != nil {
		return fmt.Errorf("load frps configuration: %w", err)
	}
	if loaded.VhostHTTPPort != 0 || loaded.VhostHTTPSPort != 0 {
		return fmt.Errorf("omit vhostHTTPPort and vhostHTTPSPort: Caddy owns the HTTP/HTTPS listeners")
	}
	if _, err := validation.NewConfigValidator(nil).ValidateServerConfig(loaded); err != nil {
		return err
	}
	app.config, app.context = loaded, ctx
	app.https = netpkg.NewInternalListener()
	return nil
}

func (app *App) Start() error {
	httpPort, httpsPort := caddyhttp.DefaultHTTPPort, caddyhttp.DefaultHTTPSPort
	value, err := app.context.AppIfConfigured("http")
	if err != nil && !errors.Is(err, caddy.ErrNotConfigured) {
		return err
	}
	if value != nil {
		httpApp := value.(*caddyhttp.App)
		if httpApp.HTTPPort != 0 {
			httpPort = httpApp.HTTPPort
		}
		if httpApp.HTTPSPort != 0 {
			httpsPort = httpApp.HTTPSPort
		}
	}
	app.config.VhostHTTPPort, app.config.VhostHTTPSPort = httpPort, httpsPort
	service, err := server.NewEmbeddedService(app.config, app.https)
	if err != nil {
		return err
	}
	log.InitLogger(app.config.Log.To, app.config.Log.Level, int(app.config.Log.MaxDays), app.config.Log.DisablePrintColor)
	app.service.Store(service)
	go service.Run(context.Background())
	return nil
}

func (app *App) Stop() error {
	if service := app.service.Swap(nil); service != nil {
		return service.Close()
	}
	return nil
}

type frpModule struct{ app *App }

func (module *frpModule) Provision(ctx caddy.Context) error {
	app, err := ctx.App("frps")
	if err != nil {
		return err
	}
	module.app = app.(*App)
	return nil
}

func (*frpModule) UnmarshalCaddyfile(dispenser *caddyfile.Dispenser) error {
	dispenser.Next()
	if dispenser.NextArg() || dispenser.NextBlock(0) {
		return dispenser.ArgErr()
	}
	return nil
}

func parseGlobal(dispenser *caddyfile.Dispenser, existing any) (any, error) {
	if existing != nil {
		return nil, dispenser.Err("frps may only be configured once")
	}
	app := new(App)
	for dispenser.Next() {
		if !dispenser.Args(&app.ConfigFile) || dispenser.NextArg() || dispenser.NextBlock(0) {
			return nil, dispenser.ArgErr()
		}
	}
	return httpcaddyfile.App{Name: "frps", Value: caddyconfig.JSON(app, nil)}, nil
}

var (
	_ caddy.App         = (*App)(nil)
	_ caddy.Provisioner = (*App)(nil)
)
