package caddyfrp

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/caddyserver/caddy/v2"
)

func TestFrpsReload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	path := filepath.Join(t.TempDir(), "frps.toml")
	writeConfig := func(token string) {
		t.Helper()
		text := fmt.Sprintf("bindAddr = '127.0.0.1'\nbindPort = %d\nauth.token = %q\n", port, token)
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("first")
	configuration := []byte(fmt.Sprintf(`{"admin":{"disabled":true},"apps":{"frps":{"config_file":%q}}}`, path))
	t.Cleanup(func() {
		if err := caddy.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err := caddy.Load(configuration, true); err != nil {
		t.Fatal(err)
	}
	current := func() *App {
		t.Helper()
		app, err := caddy.ActiveContext().App("frps")
		if err != nil {
			t.Fatal(err)
		}
		return app.(*App)
	}
	first := current()
	service := first.service.Load()
	if err := caddy.Load(configuration, true); err != nil {
		t.Fatalf("reload unchanged frps: %v", err)
	}
	second := current()
	if first == second || second.service.Load() != service || first.https != second.https {
		t.Fatal("reload did not reuse the frps service and HTTPS listener")
	}
	if references, _ := frpsInstances.References("frps"); references != 1 {
		t.Fatalf("references after reload = %d, want 1", references)
	}
	writeConfig("changed")
	if err := caddy.Load(configuration, true); err == nil {
		t.Fatal("changed frps configuration accepted without restart")
	}
	if current().service.Load() != service {
		t.Fatal("failed reload replaced the running service")
	}
	connection, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("running service unavailable after reload: %v", err)
	}
	_ = connection.Close()
	if err := caddy.Stop(); err != nil {
		t.Fatal(err)
	}
	if references, exists := frpsInstances.References("frps"); exists {
		t.Fatalf("service retained after shutdown: %d references", references)
	}
	listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("shutdown did not release port: %v", err)
	}
	_ = listener.Close()
}
