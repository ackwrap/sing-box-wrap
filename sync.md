# sync -> devel 合并记录

## 2026-07-27

- 合并目标：`sync` (`696157251c555d5acbfb618ccc030ef902502000`) -> `devel` (`d2f85e796a601348409bdd7945755b4e30cf010d`)
- merge base：`7067276170f03034017eb1887f671b7c9f11a5fd`
- 背景：远端 `sync` 重写过上游历史，大量相同上游提交具有不同哈希，因此同时出现真实行为冲突和假性 `add/add` 冲突。
- 状态：冲突已逐项处理并完成可执行验证，等待最终审核和提交。

### 采用 sync

| 文件 | 理由 |
| --- | --- |
| `common/tls/utls_client_test.go` | 测试冲突按项目规则采用 `sync`；证书 SHA-256 的核心覆盖仍由非冲突文件 `common/tls/certificate_pin_test.go` 保留。 |
| `common/tls/windows_client_test.go` | 测试冲突按项目规则采用 `sync`；避免携带被上游重写历史重复引入的测试块。 |
| `docs/changelog.md` | 文档冲突按项目规则采用 `sync`，保留最新版本记录。 |
| `docs/configuration/route/rule.md` | 文档冲突按项目规则采用 `sync`，移除 `devel` 中重复的 MAC/hostname 段落。 |
| `docs/configuration/route/rule.zh.md` | 与英文文档保持一致，采用 `sync`。 |
| `docs/configuration/shared/pre-match.md` | 文档冲突按项目规则采用 `sync`，仅规范化行尾空格。 |
| `docs/configuration/shared/pre-match.zh.md` | 与英文文档保持一致，采用 `sync`，仅规范化行尾空格。 |
| `clients/android` | 接受 `sync` 的客户端子模块指针，包含版本更新和停止服务后清理日志修复。 |
| `clients/apple` | 接受 `sync` 的客户端子模块指针，包含版本与构建流程更新。 |

### 采用 Ackwrap

| 文件 | 理由 |
| --- | --- |
| `adapter/router.go` | `sync` 的最终差异仅删除 Ackwrap 的运行时路由接口和数据结构；这些接口仍被运行时 API 使用。 |
| `route/router.go` | `sync` 的最终差异仅删除运行时出站映射、动态路由快照和访问事件状态；保留 Ackwrap 实现。 |
| `service/api/server.go` | 保留运行时 API 初始化、挂载和完整关闭错误处理；`sync` 版本会移除该功能。 |
| `service/api/web_bridge.go` | 保留运行时 API 路径、PUT/DELETE CORS 方法及桥接字段；`sync` 版本会移除该功能。 |
| `common/httpclient/apple_transport_darwin.go` | 保留 Apple HTTP 引擎对不支持 `certificate_sha256` 的明确拒绝，避免静默忽略配置。 |
| `common/tls/apple_client_platform.go` | 保留 Apple TLS 的证书 SHA-256 校验。 |
| `common/tls/reality_client.go` | 保留 Reality 对不支持证书 SHA-256 配置的明确错误。 |
| `common/tls/std_client.go` | 保留证书 SHA-256 解析、验证、空证书保护和标准 TLS 集成。 |
| `common/tls/system_client.go` | 保留系统 TLS 的证书 SHA-256 校验参数和冲突校验。 |
| `common/tls/system_client_engine.go` | 保留系统 TLS 引擎中的证书 SHA-256 状态传递。 |
| `common/tls/windows_client.go` | 保留 Windows TLS 握手后的证书 SHA-256 校验。 |
| `include/registry.go` | 保留 Ackwrap 的实际 ShadowsocksR outbound；`sync` 会退化为 removed stub。 |
| `service/oomkiller/service_darwin.go` | 保留 `CompareAndSwap`，防止并发内存压力通知重复生成 OOM 草稿；比无条件 `Store` 更安全。 |

### 手工融合

| 文件 | 融合内容 |
| --- | --- |
| `route/route.go` | 保留 Ackwrap 的运行时 lease、动态路由、节点暴露和访问事件路径；采用 `sync` 的 TUN DNS 提前劫持；采用 `sync` 的 route/route-options/bypass 统一选项应用；增加已准备元数据的匹配入口，避免运行时选路与上游 `matchRule` 改动造成重复解析。 |
| `route/runtime_routing_test.go` | Ackwrap 运行时测试改用已准备元数据的匹配入口，与融合后的生产调用路径保持一致。 |
| `go.mod` | 采用 `sync` 的 `sing`、`sing-quic`、`sing-snell`、`sing-tun` 新版本，同时保留 `github.com/sagernet/sing-vmess => github.com/ackwrap/sing-vmess` replace。 |
| `go.sum` | 采用 `sync` 新依赖校验和，同时保留 `github.com/ackwrap/sing-vmess v0.2.8-ackwrap.1` 校验和。 |

### 验证结果

- `make build` 通过。
- `go build ./...` 通过。
- `go test ./route ./common/tls ./common/httpclient ./option ./service/api ./service/oomkiller ./include ./protocol/shadowsocksr` 通过。
- `go test ./...` 除既有环境依赖外均通过：`common/tlsfragment` 依赖外部 TLS 网络，`common/tlsspoof` 和 `common/windivert` 需要管理员权限。
- `go vet ./...` 仅报告既有 `unsafe.Pointer` 告警：`daemon/managed_service.go`、`experimental/libbox/debug.go`、`experimental/boxdd/authenticode_windows.go`。
- Android/Apple 子模块工作树已分别对齐合并后的 gitlink。
- `git diff --cached --check` 通过；不存在冲突标记或未解决索引。

## 2026-07-28

- 合并目标：`origin/sync` (`455d3798d5d8f48f9ff5e44d834226eda814b7d7`) -> `devel` (`2182e6378a96ebc19b77298ba55b40207de5b29f`)。
- merge base：`c9e81856e572b332dffb8ba0a6a4ba3bee1f95d2`。
- 背景：`sync` 再次重写历史，上次已合并的 `69615725` 不再是新 `sync` 的祖先，因此 TLS、router、API 等相同定制冲突再次出现。后续应评估用可重放的 Ackwrap patch/commit 队列替代长期双向历史合并。

### 冲突决策

| 文件 | 决策与理由 |
| --- | --- |
| `adapter/router.go` | 采用 `sync` 新版 Router/PreMatch 接口，恢复 Ackwrap runtime routing 接口和数据结构。 |
| `common/httpclient/apple_transport_darwin.go` | 采用 `sync` Apple HTTP 实现，保留对不支持完整证书 SHA-256 pin 的明确拒绝，避免静默忽略。 |
| `common/tls/apple_client_platform.go` | 采用 `sync` 平台实现，恢复完整证书 SHA-256 校验和失败清理。 |
| `common/tls/reality_client.go` | 采用 `sync` Reality 实现，保留对不支持完整证书 pin 的明确错误。 |
| `common/tls/std_client.go` | 采用 `sync` 标准 TLS 实现，保留完整证书 pin 及空证书保护。 |
| `common/tls/system_client.go` | 采用 `sync` 系统 TLS 校验流程，恢复完整证书 pin 的解析、冲突检查和返回状态。 |
| `common/tls/system_client_engine.go` | 采用 `sync` 引擎结构，恢复完整证书 pin 在 clone 和构造链路中的传播。 |
| `common/tls/windows_client.go` | 采用 `sync` Schannel 实现，恢复握手后的完整证书 SHA-256 校验。 |
| `include/registry.go` | 采用 `sync` 最新注册表，恢复 Ackwrap ShadowsocksR outbound 注册。 |
| `route/route.go` | 手工融合：保留 runtime lease、动态路由、节点暴露和访问事件；采用 `sync` 新版连接处理和 route option 行为，并继续通过 `matchPreparedRule` 避免重复元数据解析。 |
| `route/router.go` | 采用 `sync` 最新生命周期和字段，恢复 runtime outbound 映射、动态路由快照与访问事件状态。 |
| `service/api/server.go` | 采用 `sync` 最新服务启动/关闭顺序，恢复 runtime API 创建、挂载和关闭错误聚合。 |
| `service/api/web_bridge.go` | 采用 `sync` 最新 gRPC-Web/WebSocket bridge，恢复 runtime API 路由及 PUT/DELETE CORS。 |
| `service/oomkiller/service_darwin.go` | 采用 `sync` 最新 OOM 实现，但保留 `CompareAndSwap` 防止并发重复生成 draft。 |
| `go.mod` | 采用 `sync` 新版 `sing-tun`，保留 Ackwrap `sing-vmess` replace。 |
| `go.sum` | 采用新版 `sing-tun` 校验和，保留 Ackwrap `sing-vmess` 校验和。 |
| `docs/configuration/shared/pre-match.md` | 文档冲突按规则采用 `sync`。 |
| `docs/configuration/shared/pre-match.zh.md` | 文档冲突按规则采用 `sync`。 |

### 验证结果

- `go test ./route ./service/api ./common/tls ./common/httpclient ./service/oomkiller ./protocol/shadowsocksr ./transport/clashssr/... ./experimental/clashapi ./protocol/vless` 通过。
- `go mod tidy` 完成，保留 Ackwrap `sing-vmess` replace，并采用 `sync` 最新 `sing-tun`。
- `make build` 通过；`go build ./...` 在 Windows 临时文件锁释放后单独重跑通过。
- `go test ./...` 除既有环境依赖外均通过：`common/tlsfragment` 依赖外部 TLS 网络，`common/tlsspoof` 和 `common/windivert` 需要管理员权限。
- `go vet ./...` 仅报告既有 `unsafe.Pointer` 告警：`daemon/managed_service.go`、`experimental/libbox/debug.go`、`experimental/boxdd/authenticode_windows.go`。
