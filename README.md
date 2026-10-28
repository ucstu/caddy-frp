# Caddy FRP

将 frps 嵌入 Caddy，共用 HTTP/HTTPS 入口。支持 HTTP 转发、TLS 透传，以及由 Caddy 管理证书和终止 TLS；frpc 登录、代理注册和 TCP/UDP 转发仍使用原生 frp。

不需要单独运行 frps，也不依赖 caddy-l4。

## 构建

构建需要 Go 1.26+ 和 xcaddy。

### 从远程仓库构建

无需克隆仓库即可构建：

```sh
xcaddy build v2.11.7 \
	--with github.com/ucstu/caddy-frp@v0.0.2 \
	--replace github.com/fatedier/frp=github.com/ucstu/frp@v0.71.1-patch \
	--output ./caddy
```

frp 的替换参数不能省略，插件依赖 patch 版本的 frp。

### 从本地源码构建

另需 Git。获取本仓库后，在仓库根目录执行：

```sh
git submodule update --init --recursive
xcaddy build v2.11.7 \
	--with github.com/ucstu/caddy-frp="$PWD" \
  --replace github.com/fatedier/frp="$PWD/frp" \
  --output ./caddy
```

构建完成后，运行生成的 Caddy 即可，不需要在服务器上保留 Go 或 xcaddy。构建使用仓库固定的 frp 子模块；不要直接替换为官方 frp，插件依赖 fork 提供的嵌入接口。

## 服务端配置

准备指向服务器的域名，开放 TCP 80、443 和 frpc 接入端口（默认 7000）。服务器需要有权限监听这些端口。TCP/UDP 代理还需开放各自使用的端口。

frps 配置可参考 [examples/frps.toml](examples/frps.toml)：

```toml
auth.token = "{{ .Envs.FRP_TOKEN }}"
transport.tls.force = true
allowPorts = [{ start = 20000, end = 20999 }]
```

两端使用相同的强随机令牌。`allowPorts` 限制客户端可以申请的 TCP/UDP 监听端口，不是 HTTP/HTTPS 域名白名单。

下面是使用自动证书的 Caddyfile，配置路径相对于运行目录：

```caddyfile
{
	frps examples/frps.toml

	on_demand_tls {
		permission frp
	}

	servers :443 {
		strict_sni_host on
		listener_wrappers {
			frp
			tls
		}
	}
}

http://, https:// {
	tls {
		on_demand
	}
	route {
		frp
		respond "not found" 404
	}
}
```

```sh
export FRP_TOKEN='替换为强随机令牌'
./caddy run --config Caddyfile
```

- 不要在 frps 配置中设置非零 `vhostHTTPPort` 或 `vhostHTTPSPort`，HTTP/HTTPS 端口由 Caddy 管理。
- 保持监听器顺序为 `frp`、`tls`，让 TLS 透传分流先于 Caddy 握手。
- 在 `route` 中按顺序放置 `frp` 和兜底处理器；未匹配的请求交给后续处理器。
- 如果增加具体域名的 HTTPS 站点，需留意 Caddy 自动重定向可能先于 HTTP 兜底路由执行。
- 自动签证需要有效 DNS、可用的证书验证方式及外网访问。只应向受信任的 frpc 开放代理注册权限，因为它也决定了可按需签证的域名范围。

## 客户端配置

客户端使用原生 frpc，无需安装 Caddy 插件。把 `serverAddr` 改成服务器地址，并将后端地址改为 frpc 所在机器可访问的服务：

```toml
serverAddr = "你的服务器地址"
serverPort = 7000
loginFailExit = false
auth.token = "{{ .Envs.FRP_TOKEN }}"

[[proxies]]
name = "web?tls=true&redirect=true"
type = "http"
localIP = "127.0.0.1"
localPort = 8080
customDomains = ["web.example.com"]
```

```sh
export FRP_TOKEN='与服务端相同的令牌'
frpc -c frpc.toml
```

这个代理由 Caddy 为 `web.example.com` 按需签证、终止 TLS，然后转发到客户端的 HTTP 服务；HTTP 请求会跳转到 HTTPS。

### TLS 透传

后端自己提供证书时，注册不带 `tls=true` 的 HTTPS 代理：

```toml
[[proxies]]
name = "passthrough"
type = "https"
localIP = "127.0.0.1"
localPort = 8443
customDomains = ["tls.example.com"]
```

用户与后端直接完成 TLS 握手。Caddy 只读取 SNI 分流，不解密，也不为该代理签证。

若改成 `name = "backend?tls=true"`，Caddy 会终止前端 TLS，再建立后端 TLS。默认要求后端证书受 Caddy 所在系统信任，并匹配请求域名。

### 自签或私有证书后端

对明确受信的 HTTPS 后端，可以显式跳过回源证书校验：

```toml
[[proxies]]
name = "backend?tls=true&insecure=true"
type = "https"
localIP = "127.0.0.1"
localPort = 8443
customDomains = ["backend.example.com"]
```

`insecure=true` 跳过后端证书链及域名校验，连接仍使用 TLS，但不能验证后端身份，有中间人攻击风险。它不是导入或固定信任某张证书；默认保持 `false`，优先使用系统信任的证书。

该参数仅作用于 HTTP handler 的 HTTPS 回源，不改变前端证书验证、TLS 透传或 frpc 控制通道。严格校验与跳过校验使用独立连接池。

## 代理名参数

参数写在 frpc 的代理 `name` 中，不是用户访问 URL 的查询参数。

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `tls` | `false` | 允许按需签证；对 HTTPS 代理选择 Caddy TLS 终止而非透传 |
| `redirect` | `false` | 将非 TLS 的 HTTP 请求以 308 跳转到 HTTPS，要求同时设置 `tls=true` |
| `insecure` | `false` | 跳过 HTTPS 回源证书链与域名校验，仅用于明确受信后端 |

每个参数只能出现一次，值只能是小写 `true` 或 `false`。重定向保留路径、查询参数和 Caddy 配置的 HTTPS 端口；HTTPS 请求不会再次跳转。

`tls=false` 不是访问控制开关：如果 Caddy 已使用现有证书完成 TLS 握手，请求仍可以转发。进入 HTTP handler 的请求按后端类型转发，HTTP 前端也可以访问 HTTPS 后端；后端 TLS 不会加密客户端到 Caddy 的明文连接。

同域名的 HTTPS 代理优先于 HTTP 代理。HTTP 代理沿用 frp 的路径、用户匹配规则。负载均衡组成员应使用相同参数。旧的 `X-Caddy-Https`、`X-Caddy-Https-Redirect` 参数不再生效。

## 排错与限制

| 现象 | 检查项 |
| --- | --- |
| HTTP 返回 404 | 域名是否匹配、frpc 是否成功注册；示例的兜底处理器返回 404 |
| HTTP 返回 502，正文提示代理配置无效 | 检查代理名参数，如重复值、非法布尔值或 `redirect=true` 缺少 `tls=true` |
| HTTPS 回源返回 502 | 检查后端可达性、证书信任及证书域名 |
| 前端 TLS 握手失败 | 检查 `tls=true`、域名注册、证书签发条件；透传代理则检查后端 TLS 服务 |
| HTTP 返回 503 | 内嵌 frps 当前不可用 |

- 插件的 HTTPS 分流只支持 TCP TLS，不提供 QUIC/HTTP/3 透传。
- frps 配置不变时，Caddy 热重载复用同一 frps 实例；修改 frps 配置或 Caddy 的 HTTP/HTTPS 端口后需重启，热重载会拒绝这些变更并保留原实例。
- frps 生命周期沿用上游。已知上游内存监听器存在并发发送与关闭竞态，本插件未修复该上游问题。

## 本地体验

仓库内的 [Caddyfile](examples/Caddyfile)、[frps 配置](examples/frps.toml) 和 [frpc 配置](examples/frpc.toml) 用于本机演示。它们使用内部 CA、自签后端和本地地址，不是可直接上线的配置。

额外准备 Make、Python 3、OpenSSL 和 curl，然后执行：

```sh
export FRP_TOKEN='替换为强随机令牌'
make dev
```

该命令启动 Caddy、frpc 和演示后端；优先使用 tmux 或 screen，均未安装时并行运行。也可分别运行 `make server`、`make client`、`make back`。另一个终端运行 `make test` 检查链路。

本地测试使用 `curl -k`，仅用于演示。HTTPS 透传预期成功；`terminated.example.com` 使用 `tls=true&insecure=true`，自签后端预期可正常转发。HTTP 入口访问未设置 `insecure=true` 的 `tls.example.com` 时，因后端证书不受信任预期返回 502，用于检查默认校验仍生效。
