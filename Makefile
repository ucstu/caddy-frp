.PHONY: server client back test dev

server:
	: "$${FRP_TOKEN:?Set FRP_TOKEN before starting Caddy}"
	xcaddy -- run -c examples/Caddyfile

client:
	: "$${FRP_TOKEN:?Set FRP_TOKEN before starting frpc}"
	go run -C frp -tags frpc,noweb ./cmd/frpc -c ../examples/frpc.toml

back:
	set -eu; directory=$$(mktemp -d); \
	trap 'kill $${http_pid:-} $${tls_pid:-} 2>/dev/null || :; rm -rf "$$directory"' EXIT; \
	trap 'exit 130' INT; trap 'exit 143' TERM; \
	openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
		-keyout "$$directory/key.pem" -out "$$directory/cert.pem" \
		-subj /CN=tls.example.com; \
	python3 -m http.server 19080 --bind 127.0.0.1 --directory examples & http_pid=$$!; \
	openssl s_server -accept 127.0.0.1:19443 -cert "$$directory/cert.pem" \
		-key "$$directory/key.pem" -www & tls_pid=$$!; \
	wait

dev:
	: "$${FRP_TOKEN:?Set FRP_TOKEN before starting dev}"
	@set -eu; session=tunnel-dev-$$$$; \
	if command -v tmux >/dev/null 2>&1; then \
		tmux -L "$$session" new-session -c "$(CURDIR)" '$(MAKE) server' \; \
			split-window -c "$(CURDIR)" '$(MAKE) client' \; \
			split-window -c "$(CURDIR)" '$(MAKE) back' \; select-layout tiled; \
	elif command -v screen >/dev/null 2>&1; then \
		screen -dmS "$$session" -t server $(MAKE) server; \
		screen -S "$$session" -X screen -t client $(MAKE) client; \
		screen -S "$$session" -X screen -t back $(MAKE) back; \
		screen -r "$$session"; \
	else \
		$(MAKE) -j3 server client back; \
	fi

test:
	@set -eu; for request in \
		'http 127.0.0.1 19080 200 /' \
		'https tls.example.com 19443 200 /' \
		'http web.example.com 80 308 /test%2Fpath?foo=bar' \
		'http tls.example.com 80 502 /' \
		'http terminated.example.com 80 200 /' \
		'http unknown.example.com 80 404 /' \
		'https web.example.com 443 200 /' \
		'https tls.example.com 443 200 /' \
		'https terminated.example.com 443 200 /'; do \
		set -- $$request; \
		status=$$(curl --noproxy '*' -ksS --max-time 20 -o /dev/null -w '%{http_code}' \
			--resolve "$$2:$$3:127.0.0.1" "$$1://$$2:$$3$$5"); \
		printf '%s://%s:%s%s: %s (expected %s)\n' "$$1" "$$2" "$$3" "$$5" "$$status" "$$4"; \
		test "$$status" = "$$4"; \
	done
