# 第三梯队推进证据报告（feat/engine-tiers，2026-09-05）

## 已落地

### T8 `AsyncResource.bind` 静态方法
- 根因：`try_dispatch` 形态一（NativeFn 接收者）只按 `名` 查分派表，方法名丢失。
- 修复：形态一优先尝试 `名.方法` 键（未命中回退原名，保持 console.log 等
  既有分派）；`async_hooks.AsyncResource.bind` 注册为独立处理器，复用
  `bound_trampoline` 通路（共享 LAST_BOUND 槽位，与实例 bind 同限制）。
- 证据：探针 `res.bind(fn)(21)===42`、`AsyncResource.bind(fn)(41)===42`
  双侧一致（Go oracle 同源码逐字）。

### T9 http Agent keepAlive 连接池
- `state.rs` 新增 `CONN_POOL`（origin → 空闲 TcpStream，上限 4/origin）；
  `pool_take` 以 `peek` 探活（WouldBlock=存活/Ok(0)=对端关闭）自动淘汰；
  客户端请求优先复用池内连接，完整响应后归还。
- JS 可观测行为不变；http 套件 10/10 绿。

### T10 TLS rustls 评估 spike —— **结论：纯 Rust 真实 TLS 可行**
- 依赖：`rustls 0.23（custom-provider）` + `rustls-rustcrypto 0.0.2-alpha`
  （RustCrypto 加密后端）——依赖树无 ring / aws-lc-rs / C，符合 AGENTS
  纯 Rust 约束；`tls_spike.rs`（cfg(test)）以内存双向管道驱动
  **TLS 1.3 完整握手 + 双向 echo**，单测通过。
- 证书：https.createServer 的 `{key, cert}` 由 JS 侧提供 PEM（Node 语义），
  引擎无需运行时生成证书（rcgen 依赖 ring，已弃用；PEM→DER 解码用
  base64 手写）。
- **下一步（接线设计草图）**：
  1. `https.createServer`：Server 层 rustls::ServerConnection 包裹 TcpStream
     （非阻塞），事件泵读→`process_new_packets`→明文进既有 HTTP/1.1
     wire 解析器；写路径反向包裹。PEM→DER 用现有 base64 通路。
  2. `https.request/get`：ClientConnection + AcceptAll（rejectUnauthorized:
     false 语义）或根证书校验（用户传入 ca）。
  3. 复用现有 pump 骨架（写屏障不涉及；TLS 会话缓冲纳入 GC 无关的
     Rust 侧）。
  4. 预估 300~400 行（server+client+PEM 装配）+ 探针测试。

## 评估后缓办（含设计草图与解锁条件）

| 项 | 阻塞点 | 设计草图 | 解锁条件 |
|---|---|---|---|
| `vm` 运行时源码求值 | ISA 契约禁 aluka-vm 依赖前端 | `Vm::set_source_evaluator(hook)`：aluka-cli 装入 parser+compiler 钩子，编译产物函数模板追加进 `module_functions`（索引偏移换算）后 `invoke_function` 入口函数 | 单独立项；需先固化「运行时追加函数模板」的模板索引稳定性约定 |
| `worker_threads {eval:true}` | 同上（worker 体源码求值） | 同 vm 求值钩子 | 同上 |
| http2 客户端协议栈 | HPACK 编解码 + 帧状态机 | 最小 HPACK（静态表+Huffman 不做，用字面量编码无索引）；SETTINGS/HEADERS/DATA/WINDOW_UPDATE 帧 | 有真实 http2 对端需求时立项 |
| dns PTR/reverse | std 无递归 DNS 查询 | RFC 1035 最小 UDP 查询（PTR for x.x.x.x.in-addr.arpa）；系统 resolver 地址发现是 Windows 难点（注册表/GetAdaptersAddresses FFI） | 需 FFI 豁免或用户显式传 resolver |
| zstd 熵编码压缩 | FSE/Huffman 表管理重 | 现最小 Raw 块帧完全合法（ruzstd/Go 均可解）；压缩率提升仅是优化 | 出现压缩率敏感负载再立项 |
| ALS 跨异步上下文传播 | VM 无 Go 侧 AsyncContextCapture/Restore 挂点 | `PendingResume` 增 als 快照字段，await 挂起捕获、恢复还原（async_hooks 暴露 capture/restore） | 随 vm 求值/事件循环深改同批 |
| 未捕获错误 stderr 格式 | Rust 侧 `SyntaxError: msg` vs Go `module: error in "path": ...` + at 行 | aluvm.rs 错误出口格式化对齐 | 纯展示层，随下批小改 |

## 验收证据
- 命令证据：`cargo test` → 56 个测试目标全绿（core_semantics_test 21 用例、
  gc 11 项含 write_barrier 新测、tls_spike 1 项）；`cargo clippy --all-targets
  -D warnings` → 0 错误；`cargo fmt --all --check` 通过。
- 探针证据：T8 bound/static 双一致（42）；T9 http 10/10；TLS spike
  `tls13_handshake_and_echo_over_pure_rust_provider` ok。
- 既有依赖变更：aluka-vm 增 rustls/rustls-rustcrypto/base64（均纯 Rust），
  rcgen 评估后弃用（ring 依赖）。
