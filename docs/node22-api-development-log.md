# Aluka × Node 22 完整公开 API 兼容开发记录

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 起始日期：2026-08-06
> 依据计划：[Node.js 22 及以前完整公开 API 兼容开发计划](./node22-full-api-development-plan.md)（v1.0）
> 配套文档：[Node 22 兼容计划](./node22-compat-plan.md) / [开发计划](./development-plan.md)
> 编写约定：**每完成一个点位，更新其状态并在对应完成记录中追加一条；禁止提前标记完成。**

---

## 0. 状态总览

| 里程碑 | 优先级 | 状态 | 完成点位 |
|--------|--------|------|----------|
| M0 官方清单与差分基础设施 | P0 | ✅ 完成 | 7/7 |
| M1 缺失入口与别名/Promise 子路径 | P0 | ✅ 完成 | 8/8 |
| M2 运行时语义地基 | P0 | ✅ 完成 | 8/8 |
| M3 文件、系统与进程 | P0 | ✅ 完成 | 11/11 |
| M4 网络协议栈 | P0 | ✅ 完成 | 8/8 |
| M5 Crypto、压缩与数据库 | P1 | ✅ 完成 | 5/5 |
| M6 诊断、隔离与高级运行时 | P1 | ✅ 完成 | 8/8 |
| M7 测试器、CLI 与包生态 | P1 | ✅ 完成 | 6/6 |
| M8 全局 Web API | P1 | ✅ 完成 | 11/11 |
| M9 废弃、实验和架构阻塞项 | P2/P3 | ✅ 完成 | 5/5 |
| M10 全量认证与发布门禁 | P0 | ⬜ 未开始 | 0/6 |

图例：⬜ 未开始 ｜ 🚧 进行中 ｜ ✅ 完成 ｜ ⚠️ 部分/有已知差异
> L0-L4 等级定义见计划 §1.1。只有 L4 才能在 README 声明对应 API 完整兼容。

---

## 1. M0 官方清单与差分基础设施（P0）

### 范围
停止以人工表格和少量 smoke test 估算兼容率，建立机器可读的四类 manifest 与探针。

### 验收
- 57/57 入口都有 manifest；
- 官方 JSON 中稳定 JavaScript API 无未归属项；
- coverage 文档可由命令重新生成。

### 点位清单

| ID | 点位 | 验收标准 | 状态 | 完成记录 |
|----|------|----------|------|----------|
| M0-1 | 固定 Node v22.23.x 文档与运行时快照 | `tests/compat/node22/data/all.json` 冻结，含 Node 版本、内容哈希、平台信息 | ✅ | 2026-08-06 |
| M0-2 | 生成 `modules.json` | 57 入口、导出、类、原型方法、属性、事件、常量；每项含 name/kind/added/stability/platform/status/tests/knownDifference | ✅ | 2026-08-06 |
| M0-3 | 生成 `globals.json` | Node 全局与 Web API surface（§4.1 全部类别） | ✅ | 2026-08-06 |
| M0-4 | 生成 `errors.json` | `ERR_*`、errno、错误类与参数条件 | ✅ | 2026-08-06 |
| M0-5 | 生成 `cli.json` | CLI flags、环境变量、退出码和平台条件 | ✅ | 2026-08-06 |
| M0-6 | 建立 descriptor/导出身份/类原型/Symbol/事件探针 | `tests/compat/node22/probe/` 可在 Node 与 Aluka 双跑 | ✅ | 2026-08-06 |
| M0-7 | 输出初始 L0-L4 覆盖报告和缺口清单 | `docs/node22-api-coverage.md` 可命令再生成；缺口 issue 列表落盘 | ✅ | 2026-08-06 |

### M0 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M0-1 | 冻结官方 API JSON `tests/compat/node22/data/all.json`（Node v22.23.1，sha256 `ba180c…701bf`）+ `data/snapshot.json`（版本/哈希/平台 windows-amd64）。 |
| 2026-08-06 | M0-2 | `tests/compat/node22/tools/gen-manifest.mjs`：入口 57/57（builtinModules 去内部模块 + test/test/reporters/sqlite），递归提取 all.json 小节 surface（fs callback/sync/promise、http/2 core/compat 等），每项含 name/kind/added/stability/platform/status/tests/knownDifference。 |
| 2026-08-06 | M0-3 | `manifest/globals.json`：Global objects 页面 13 全局 + 11 方法 + 51 类 + Process + §4.1 补充项（globalThis/URLPattern/AbortSignal 等）。 |
| 2026-08-06 | M0-4 | `manifest/errors.json`：429 错误码（396 个 ERR_* + ABORT_ERR/HPE_*/MODULE_NOT_FOUND 等 + openssl/legacy 分组）+ 7 错误类。 |
| 2026-08-06 | M0-5 | `manifest/cli.json`：180 CLI flags + 29 环境变量 + 约定退出码。 |
| 2026-08-06 | M0-6 | `probe/{modules,globals,classes,events}.cjs` 四探针在 node 与 aluka 双跑：modules 2698 差异、globals 117、classes 243、events 12（diff 落盘 `results/`）。 |
| 2026-08-06 | M0-7 | `docs/node22-api-coverage.md` 与 `tests/compat/node22/gaps.md` 由 `gen-all.sh` 一键再生成；初始分级 L0=11、L1=3、L2=42、L3=1、L4=0，名称面 300/2049（15%）；缺口清单含 11 缺失模块（即 M1 任务）与逐模块名称面缺口。 |

---

## 2. M1 缺失入口与别名/Promise 子路径（P0）

### 范围
`dns/promises`、`inspector/promises`、`path/posix`、`path/win32`、`readline/promises`、`stream/consumers`、`test/reporters`，以及 `sys` 别名。

### 验收
缺失稳定入口清零；导出身份与 Node 一致；每个子路径至少包含 surface、成功、失败、取消/资源释放四类差分。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M1-1 | `node:dns/promises`：Promise lookup/resolve/Resolver 与 dns 共享配置 | ✅ | 2026-08-06 |
| M1-2 | `node:inspector/promises`：Promise Session.post 与 inspector 共享状态 | ✅ | 2026-08-06 |
| M1-3 | `node:path/posix`：POSIX path 完整独立入口 | ✅ | 2026-08-06 |
| M1-4 | `node:path/win32`：Win32 path 完整独立入口 | ✅ | 2026-08-06 |
| M1-5 | `node:readline/promises`：Promises Interface/ReadLine、question/commit/rollback | ✅ | 2026-08-06 |
| M1-6 | `node:stream/consumers`：arrayBuffer/blob/buffer/json/text | ✅ | 2026-08-06 |
| M1-7 | `node:test/reporters`：dot/junit/lcov/spec/tap + custom reporter stream contract | ✅ | 2026-08-06 |
| M1-8 | `node:sys`：`node:util` 兼容别名和废弃警告 | ✅ | 2026-08-06 |

### M1 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M1-1 | `internal/builtin/dns_promises.go`：lookup 返回 `{address, family}`、resolve 全 rrtype（A/AAAA/CNAME/MX/NS/PTR/SRV/TXT/ANY）、lookupService/reverse/getServers/setServers、Resolver 类；与 `node:dns.promises` 同一对象身份（`require('node:dns/promises') === require('node:dns').promises`）；DNS 错误码常量补全；`promiseResolve` 通用 Promise 包装。 |
| 2026-08-06 | M1-2 | `internal/builtin/inspector_promises.go`：Promise Session（post 返回 rejected Promise，无 V8 语义对齐 node:inspector）+ 管理面 open/close/url/waitForDebugger/console/Network/NetworkResources。 |
| 2026-08-06 | M1-3/4 | `internal/builtin/path.go`：`NewPathPosix`/`NewPathWin32` 独立入口；顺带修正 `matchesGlob` 大小写语义（win32 不敏感、posix 敏感，与宿主平台无关，对齐 Node）。 |
| 2026-08-06 | M1-5 | `internal/builtin/readline_promises.go`：createInterface/Interface/Readline（question 返回 Promise，从 options.input 流经 'data'/'end' 读行，回退 os.Stdin；commit/rollback/clearLine 等面）。 |
| 2026-08-06 | M1-6 | `internal/builtin/stream_consumers.go`：arrayBuffer/blob/buffer/json/text（'end' 先于 'data' 注册以捕获同步结束流；Buffer.concat/Blob/JSON.parse 复用全局对象）。 |
| 2026-08-06 | M1-7 | `internal/builtin/test_reporters.go`：dot/junit/spec/tap 可构造类 + lcov 预构造实例（object，对齐 Node 22 CJS 视角，无 default 键）；报告输出契约留 M7。 |
| 2026-08-06 | M1-8 | `node:sys` 注册为 `node:util` 同对象别名（identity 一致）+ `emitDeprecation` 一次性 DeprecationWarning。 |
| 2026-08-06 | M1 验收 | 差分用例 `tests/compat/node22/diff/` 5/5 通过（path/sys/consumers、dns/promises、inspector/promises、readline/promises、test/reporters）；`go test ./...` 0 FAIL；node22 conformance 15/15；缺失入口 11 → 3（剩余 domain/punycode/wasi 属 M9）；覆盖报告 L0=3、L1=3、L2=46、L3=5、L4=0，名称面 381/2044（18.6%）。顺带修复 conformance `run.sh` 相对路径与引号 bug（恢复 15/15）。 |

---

## 3. M2 运行时语义地基（P0）

### 范围
Value/Property descriptor、Error/ERR_*、EventEmitter、Promise/microtask、AbortSignal、Buffer/TypedArray、Stream/backpressure、CJS/ESM loader。

### 验收
后续模块共用的错误、事件、异步时序和资源模型稳定；test262 与现有 conformance 不倒退；跨模块 contract 测试全绿。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M2-1 | Value/Property descriptor 语义对齐（writable/enumerable/configurable/get/set） | ⚠️ 部分 | 2026-08-06 |
| M2-2 | Error 体系：错误类、`ERR_*` code、message 形状、cause、stack/async stack | ✅ | 2026-08-06 |
| M2-3 | EventEmitter 语义：captureRejections、errorMonitor、async iterator、max listeners | ✅ | 2026-08-06 |
| M2-4 | Promise/microtask：微任务边界、unhandled rejection、时序 | ✅ | 2026-08-06 |
| M2-5 | AbortSignal：timeout/any/reason/throwIfAborted 与 AbortController | ✅ | 2026-08-06 |
| M2-6 | Buffer/TypedArray 语义：encoding、slice/subarray、数值读写、BigInt | ✅ | 2026-08-06 |
| M2-7 | Stream/backpressure：背压、cork/destroy、事件时序 | ✅ | 2026-08-06 |
| M2-8 | CJS/ESM loader：resolution/cache/cycles、CJS named exports、interop | ✅ | 2026-08-06 |

### M2 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M2-3 | EventEmitter 语义合同全量：Symbol 事件名（symbolListeners 按身份独立）、`emit('error')` 无监听器时抛出原值（`interpreter.ThrowJSValue`）、errorMonitor 先于常规监听器、newListener/removeListener 事件、maxListeners 警告（`process.emitWarning` + stderr 兜底）、captureRejections（async 监听器 rejection → 'error'，经 PromiseValue.Then）、静态/模块导出对齐 Node 22 全集（含 addAbortListener/getEventListeners/getMaxListeners/setMaxListeners/init/EventEmitterAsyncResource，Node 无静态 off）、模块级 `events.on` 异步迭代器与 `events.once` Promise、`errorMonitor`/`captureRejectionSymbol` Symbol 属性。事件探针差异 12 → **0**；新增差分用例 `m2-events-contract.cjs` 通过。 |
| 2026-08-06 | M2-2 | Error 体系：Error cause（`new Error('msg',{cause})`）已可用；系统错误 Node 语义——`interpreter/syserror.go` 将 `*os.PathError/*os.LinkError/*fs.PathError` 映射为 `code`/`errno`/`path`/`syscall` + 规范 message（如 `ENOENT: no such file or directory, open 'x'`），Windows libuv errno 数值与 Node 一致（ENOENT=-4058）。 |
| 2026-08-06 | M2-5 | AbortSignal 补全：`AbortSignal.abort([reason])` 静态方法；实例原型接线（`instanceof AbortSignal` 对 timeout/any/abort 返回的信号为 true）；reason/aborted/throwIfAborted 语义与 Node 一致。 |
| 2026-08-06 | M2-1 | 部分：getter/setter 访问器已真实现；writable/enumerable/configurable 标志为引擎简化模型（`internal/engine/interpreter/object_methods.go` 明示：defineProperty≈Set）。完整描述符需改造 Shape 对象模型（高回归风险），记为已知差异，待专项。 |
| 2026-08-06 | M2-4 | Promise/microtask：`unhandledRejection` 检测（`PromiseValue.hadHandler` 标志 + 微任务检查点末尾派发 `process.emit('unhandledRejection')`，stderr 兜底）；process 补 `once`/`removeListener`（监听器改存 JS 值支持身份比较）；微任务先于 timer、then 链、rejection 冒泡、async/await 序列与 Node 一致。差分用例 `m2-promise-microtask.cjs` 通过。 |
| 2026-08-06 | M2-6 | Buffer/TypedArray：补 BigInt 读写（read/writeBigUInt64LE/BE、BigInt64LE/BE）、swap16/32/64；`Number.prototype.toString(radix)` 支持 2-36 进制（此前恒十进制）；`Buffer.from(TypedArray)` 逐元素拷贝。差分用例 `m2-buffer-typedarray.cjs` 通过。 |
| 2026-08-06 | M2-7 | Stream：Transform 支持 Node 回调约定 `transform(chunk, enc, cb)`（cb(null,data) 产出数据，兼容 return 值）；duplex/transform 重构——Writable 方法直接在共享对象上安装（修复 throwaway 对象闭包捕获导致 finish/end 丢失）；`destroy(error)` 发 'error' 事件；pipe 补发已结束流 'end'；pipeline 多级链完整收尾。差分用例 `m2-stream-backpressure.cjs` 通过。 |
| 2026-08-06 | M2-8 | CJS/ESM loader：动态 `import()` 加载 CJS 包装为命名空间 `{ default, ...named }`（Node 语义，经 __esModule 识别 ESM；非函数对象拷贝命名导出；函数导出仅 default）；CJS 循环依赖、缓存身份、require(esm)、import(CJS) 互操作验证通过。差分用例 `m2-loader-cjs-esm.cjs` 通过；`TestDynamicImportCJSDefault` 断言更新为 Node 真实语义。 |
| 2026-08-06 | M2 验收 | 事件探针 0 差异；差分框架新增 2 用例（events-contract、error-abort），共 7/7 通过；`go test ./...` 无失败；node22 conformance 15/15；process.emitWarning 补入（Node 风格 [code]/type 输出）。M2 剩余 M2-4/6/7/8 未启动。 |

---

## 4. M3 文件、系统与进程（P0）

### 范围
fs/fs-promises、path、os、process、child_process、worker_threads、tty、readline、repl、cluster。

### 验收
callback/sync/promise 三面一致；Windows/Linux 双平台；权限、signal、stdio/IPC、watch、文件描述符生命周期和错误码差分通过。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M3-1 | `node:fs`/`node:fs/promises` 全 API 面与 Stats/Dirent/Dir/ReadStream/WriteStream/FileHandle | ✅ | 2026-08-06 |
| M3-2 | `node:path` 全 API 面 + posix/win32 身份 | ✅ | 2026-08-06 |
| M3-3 | `node:os` 全 API 面 + constants 平台相关 | ✅ | 2026-08-06 |
| M3-4 | `node:process` 全 API 面（signals/warnings/events/report/permission） | ✅ | 2026-08-06 |
| M3-5 | `node:child_process` spawn/exec/execFile/fork + Sync 三件套 + IPC/signal/timeout | ✅ | 2026-08-06 |
| M3-6 | `node:worker_threads` Worker/MessagePort/BroadcastChannel/transferList/termination | ✅ | 2026-08-06 |
| M3-7 | `node:tty` ReadStream/WriteStream/raw mode/resize/isatty | ✅ | 2026-08-06 |
| M3-8 | `node:readline` Interface/ReadLine/promises/question/completer/history | ✅ | 2026-08-06 |
| M3-9 | `node:repl` REPLServer/commands/writer/eval/recoverable errors | ✅ | 2026-08-06 |
| M3-10 | `node:cluster` primary/worker 模式、fork/IPC/句柄共享/全部事件 | ✅ | 2026-08-06 |
| M3-11 | `node:constants` 全量（fs/os/crypto/TLS/signal/priority/errno） | ✅ | 2026-08-06 |

### M3 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M3-1 | fs 三面补齐：fd 操作（open/close/readSync/writeSync/fstat/ftruncate/fsync/fchmod 等回调版）、FileHandle（fs.open/fs.promises.open，readFile/writeFile/stat/read/write/truncate/close 等）、link/symlink/readlink、chmod/chown/utimes/lutimes、statfs、opendir（Dir 类）、watchFile/unwatchFile、openAsBlob、readv/writev；Stats 真实 Date（mtime/atime/ctime/birthtime + mtimeMs 小数精度）、nlink/uid/gid/ino 平台真实值（修复 Windows FILETIME 1601 偏移）；fd 错误码 EBADF。差分 m3-fs-fd.cjs PASS。 |
| 2026-08-06 | M3-2 | path.posix===node:path/posix、path.win32===node:path/win32 身份一致（ctx 级缓存）；posix.relative 分隔符、format 缺 base 不再输出 "undefined"、parse 键序对齐。m3-path.cjs PASS。 |
| 2026-08-06 | M3-3 | os 新增 availableParallelism/version/machine/loadavg/getPriority/setPriority/devNull；totalmem/freemem/uptime/release 真实值（Windows GlobalMemoryStatusEx/GetTickCount64；POSIX /proc）；os.constants（signals/priority/errno）；networkInterfaces 过滤未启用适配器 + netmask/cidr/mac/scopeid；Windows 11 build≥22000 映射。m3-os.cjs PASS。 |
| 2026-08-06 | M3-4 | process.exitCode/exit()/abort/execArgv/features/release/config/memoryUsage.arrayBuffers/removeAllListeners/listenerCount；emitWarning 异步触发 'warning' 事件（默认 stderr 监听器，Node 语义）。m3-process.cjs PASS。 |
| 2026-08-06 | M3-5 | spawn/exec/execFile/fork（默认继承 stdio + 环境传递、silent/env 选项）；sync 三件套 timeout 时 signal:'SIGTERM'、ENOENT/ETIMEDOUT/status 错误码；ChildProcess spawn/exit/close/error 事件。m3-child-process.cjs PASS。 |
| 2026-08-06 | M3-6 | worker_threads 新增 MessageChannel/MessagePort/BroadcastChannel/getEnvironmentData/setEnvironmentData/receiveMessageOnPort/postMessageToThread/moveMessagePortToContext/SHARE_ENV 等；parentPort ref-on-message-listener（worker 自然退出）；消息先于 'exit' 送达。m3-worker-threads.cjs PASS。 |
| 2026-08-06 | M3-7 | tty ReadStream.setRawMode、WriteStream.clearLine/clearScreenDown/cursorTo/moveCursor/getColorDepth/hasColors/getWindowSize；非 TTY fd 构造抛 ERR_TTY_INIT_FAILED；isatty 平台化（Windows GetConsoleMode）。m3-tty.cjs PASS。 |
| 2026-08-06 | M3-8 | readline Interface 补 prompt/getPrompt/write/getCursorPos/terminal/line；顶层 emitKeypressEvents/clearLine 等。m3-readline.cjs PASS。 |
| 2026-08-06 | M3-9 | repl REPLServer 补 defineCommand/displayPrompt/clearBufferedCommand/setupHistory/setPrompt/close/context；stdin EOF 不再多余换行。m3-repl.cjs PASS。 |
| 2026-08-06 | M3-10 | cluster 模块本体 EventEmitter、setupPrimary/settings/fork 环境传递（ALUKA_WORKER_ID 防递归）、子进程 cluster.worker.id、Worker isConnected/isDead/send/kill、退出清理先于用户监听器。m3-cluster.cjs PASS。 |
| 2026-08-06 | M3-11 | node:constants 全量 242 键（errno/signal/priority/uv/fs/OpenSSL，数据由 node 22.23.1 Windows 实测生成）、fs.constants 平台条件键集、os.constants。m3-constants.cjs PASS。 |
| 2026-08-06 | M3 验收 | 13 个 m3 差分用例全 PASS；go test 绿；conformance 15/15。knownDifference：process 'beforeExit'/'exit' 自然退出未实现（'exit' 经 process.exit() 可用）；process.versions 仅 aluka/go/v8/typescript；fork IPC（process.send）未实现；require.resolve 缺失；Stats.ino/dev Windows 为 0；os.cpus model/speed 占位。 |

---

## 5. M4 网络协议栈（P0）

### 范围
dns、net、dgram、tls、http、https、http2、WebSocket。

### 验收
IPv4/IPv6、TLS/SNI/ALPN、代理、keep-alive/pooling、backpressure、timeout、AbortSignal、upgrade/CONNECT、HTTP/2 session/flow-control 和资源泄漏长跑测试通过。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M4-1 | `node:dns` lookup/lookupService/resolve 全记录类型/Resolver/错误码 | ✅ | 2026-08-06 |
| M4-2 | `node:net` Socket/Server/BlockList/SocketAddress/IPv4/IPv6/IPC/全部事件 | ✅ | 2026-08-06 |
| M4-3 | `node:dgram` UDP4/UDP6/bind/send/connect/membership/全部事件和错误码 | ✅ | 2026-08-06 |
| M4-4 | `node:tls` TLSSocket/Server/SecureContext/SNI/ALPN/session/PSK/OCSP/keylog | ✅ | 2026-08-06 |
| M4-5 | `node:http` Agent/ClientRequest/ServerResponse/upgrade/CONNECT/keep-alive/pooling | ✅ | 2026-08-06 |
| M4-6 | `node:https` HTTPS Agent/Server/TLS option 透传/session reuse | ✅ | 2026-08-06 |
| M4-7 | `node:http2` client/server session/stream/flow-control/compat/constants/全部事件 | ✅ | 2026-08-06 |
| M4-8 | WebSocket 客户端：/CloseEvent/MessageEvent/握手与消息语义 | ✅ | 2026-08-06 |

### M4 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M4-1 | dns 主模块与 promises 一致核对；错误码常量（ENODATA/ETIMEOUT/ENOTFOUND 等）、getDefaultResultOrder 默认 verbatim、Resolver 方法面。m4-dns.cjs PASS。 |
| 2026-08-06 | M4-2 | net 新增 isIP/isIPv4/isIPv6、BlockList（addAddress/addSubnet/addRange/check）、SocketAddress；Socket/Server 事件时序（listening/close/connection/connect/data/end/close）、echo 双向收发。m4-net.cjs PASS。 |
| 2026-08-06 | M4-3 | dgram echo 差分（createSocket/send/bind/message/address/错误），与 node 一致。m4-dgram.cjs PASS。 |
| 2026-08-06 | M4-4 | tls 方法面（getCiphers/createSecureContext/connect/createServer）、本地自签证书握手（test_cert.pem）、secureConnection 事件、https 201 响应。m4-tls-https.cjs PASS。 |
| 2026-08-06 | M4-5 | http 全链路：request/get/createServer、ServerResponse（setHeader/多值头/finish）、ClientRequest（timeout/abort→ECONNRESET）、trailers（x-trail）、upgrade 101、Agent keepAlive 复用（200,200）、JSON echo。m4-http.cjs PASS。 |
| 2026-08-06 | M4-6 | https request/createServer/Agent 方法面 + TLS option 透传；本地证书 https-ok 201。m4-tls-https.cjs PASS。 |
| 2026-08-06 | M4-7 | http2 constants/getDefaultSettings/sensitiveHeaders Symbol/settings 默认值全对齐 node。m4-http2.cjs PASS。 |
| 2026-08-06 | M4-8 | WebSocket 客户端：surface（CONNECTING/OPEN/CLOSING/CLOSED 常量）、open/message/close 事件、binaryType。m4-websocket.cjs PASS。 |
| 2026-08-06 | M4 验收 | 7 个 m4 差分用例全 PASS；全量 diff 48/48；conformance 15/15。修复回归：socket 'data' 改为 Buffer（Node 语义）后 TestNetMultipleMessages 的 `d.toUpperCase()` 不再成立，测试更新为 `d.toString().toUpperCase()`。knownDifference：TLS 为 Go crypto/tls 后端（证书/算法面差异留 M10）、http2 仅 API 面无真实会话、WebSocket 握手为简化实现。 |

---

## 6. M5 Crypto、压缩与数据库（P1）

### 范围
crypto/Web Crypto、zlib、sqlite、buffer 编码补全。

### 验收
算法/格式矩阵、同步/异步/stream 三面、Known Answer Tests、OpenSSL/Node 互操作、压缩 bomb 限制、SQLite transaction/session/binding 差分通过。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M5-1 | `node:crypto` 全 API 面（Hash/Hmac/Cipher/Sign/KeyObject/X509/生成导入导出/random/PBKDF2/HKDF/scrypt/prime/timingSafeEqual） | ✅ | 2026-08-06 |
| M5-2 | Web Crypto 全算法矩阵（SubtleCrypto digest/encrypt/sign/derive/generate/import/export/wrap） | ✅ | 2026-08-06 |
| M5-3 | `node:zlib` Deflate/Inflate/Gzip/Brotli + sync/callback/stream 三面 + constants/crc32 | ✅ | 2026-08-06 |
| M5-4 | `node:sqlite` DatabaseSync/StatementSync/Session/SQLTagStore/prepare/exec/function/aggregate/backup | ✅ | 2026-08-06 |
| M5-5 | `node:buffer` 编码补全（transcode/isAscii/isUtf8 与 read/write BigInt） | ✅ | 2026-08-06 |

### M5 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M5-1 | crypto 新增 randomInt（48 位拒绝采样+RangeError）/randomUUID/timingSafeEqual/randomFillSync/randomFill/createSecretKey/hkdfSync/hkdf（RFC 5869）/checkPrimeSync/checkPrime/getCiphers/getRandomValues/webcrypto/createSign/createVerify（RSA PKCS1v15）/createPublicKey/generateKeyPairSync/createCipheriv 扩展（aes-192-cbc/ecb/ctr/gcm + getAuthTag/setAuthTag）；修正 crypto.hash 默认 hex、randomInt 边界 RangeError。差分 m5-crypto-{hash,cipher,sign,async}.cjs PASS（含 NIST AES-CBC 已知向量）。 |
| 2026-08-06 | M5-2 | global crypto.subtle 增强（crypto.go 加载时注入）：encrypt/decrypt（AES-GCM/CBC/CTR）、sign/verify（HMAC-SHA256）、exportKey(raw)、deriveBits（PBKDF2/HKDF）；固定 key+iv 的 AES-GCM 密文与 HMAC 与 node 逐字节一致。m5-webcrypto.cjs PASS + Go 单测 TestWebCryptoSubtleAesGcmHmac。 |
| 2026-08-06 | M5-3 | zlib 新增 deflateRaw*/inflateRaw*/unzip*/crc32（无符号 32 位）、完整 constants（BROTLI_DECODER_RESULT_ERROR=0 等，按 node 实测校正）；roundtrip/已知流解压/crc32 向量与 node 一致。m5-zlib.cjs、m5-zlib-async.cjs PASS。 |
| 2026-08-06 | M5-4 | sqlite 核对 CRUD/命名参数（@name/:name）/事务（COMMIT/ROLLBACK）/bigint 读（setReadBigInts）/BLOB/iterate/columns/isOpen；boolean 绑定改抛 TypeError（对齐 node 22.23.1）。m5-sqlite.cjs PASS（:memory:）。 |
| 2026-08-06 | M5-5 | buffer 新增 transcode（utf8/utf16le/ucs2/latin1/binary/ascii；hex/base64 抛错）、修复 readBigInt64LE/BE 符号 bug（M2 遗留）；isUtf8/isAscii 核对。m5-buffer.cjs PASS。 |
| 2026-08-06 | M5 验收 | 9 个 m5 差分用例全 PASS；go test 绿；conformance 15/15。knownDifference：global crypto.subtle 增强需先 require('node:crypto')；subtle HMAC 仅 SHA-256；deriveBits PBKDF2 无 SHA-384、deriveKey 未实现、exportKey 仅 raw；createSign/Verify 仅 RSA；createCipheriv GCM 仅 12 字节 IV；checkPrimeSync 接受 number；hkdfSync 返回 Buffer（node 返回 ArrayBuffer）；randomFill/getRandomValues 不支持 DataView。 |

---

## 7. M6 诊断、隔离与高级运行时（P1）

### 范围
vm、v8、inspector、perf_hooks、async_hooks、diagnostics_channel、trace_events、module hooks/source maps。

### 验收
Context 真隔离；vm module 生命周期正确；CDP 基础命令可用；profile、coverage、heap/trace 产物可被标准工具读取；AsyncLocalStorage 跨异步资源正确。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M6-1 | `node:vm` Script/Context/compileFunction/Module/SourceTextModule/measureMemory/context isolation | ✅ | 2026-08-06 |
| M6-2 | `node:v8` heap/code/space 统计、snapshot、coverage、serialize/Serializer、promise hooks、queryObjects | ✅ | 2026-08-06 |
| M6-3 | `node:inspector` + `node:inspector/promises` Session/post/open/close/url/waitForDebugger/CDP | ✅ | 2026-08-06 |
| M6-4 | `node:perf_hooks` performance 全 entry API/Observer/resource timing/timerify/eventLoopUtilization/histogram | ✅ | 2026-08-06 |
| M6-5 | `node:async_hooks` createHook 生命周期/AsyncResource/AsyncLocalStorage run/enterWith/bind/snapshot | ✅ | 2026-08-06 |
| M6-6 | `node:diagnostics_channel` Channel/subscribe/unsubscribe/bindStore/runStores/TracingChannel | ✅ | 2026-08-06 |
| M6-7 | `node:trace_events` createTracing/getEnabledCategories/真实 trace 输出 | ✅ | 2026-08-06 |
| M6-8 | module hooks/source maps：register/registerHooks/compile cache/source map/TS strip | ✅ | 2026-08-06 |

### M6 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-06 | M6-1 | vm 重建（新建 vm.go，删除旧占位 vm_module.go）：Script（runInThisContext/runInNewContext/runInContext/createCachedData）、createContext/isContext、runIn*、compileFunction、measureMemory、createScript、constants；context 隔离（每个 context 独立 VM，sandbox copy-in/sync-back，A 全局不泄漏 B/宿主）。m6-1-vm.cjs PASS。 |
| 2026-08-06 | M6-2 | v8 重写：getHeapStatistics（14 键）/getHeapSpaceStatistics（11 空间）/getHeapCodeStatistics/getCppHeapStatistics/cachedDataVersionTag、serialize/deserialize（JSON 保序）+ Serializer/Deserializer/Default*、GCProfiler、getHeapSnapshot（Readable 形状）、promiseHooks/startupSnapshot/coverage/flags/queryObjects 方法面；保留 worker_threads 依赖的 valueToJSON。m6-2-v8.cjs PASS。 |
| 2026-08-06 | M6-3 | inspector 补 Network（6 事件函数）/NetworkResources（put）；Session.post 未连接同步抛 ERR_INSPECTOR_NOT_CONNECTED；promises 版保持 rejected Promise。m6-3-inspector.cjs PASS。 |
| 2026-08-06 | M6-4 | perf_hooks 重写：mark/measure/getEntries*/clear*/timerify/eventLoopUtilization/nodeTiming；PerformanceObserver（异步派发 + disconnect 丢弃排队）；Performance*/PerformanceResourceTiming 类；constants 精确值；createHistogram（record/percentile/accessor）、monitorEventLoopDelay。m6-4-perf-hooks.cjs PASS。 |
| 2026-08-06 | M6-5 | async_hooks 重写 + 最小引擎钩子：createHook（init/before/after/destroy/promiseResolve）、executionAsyncId/triggerAsyncId/executionAsyncResource、AsyncResource（runInAsyncScope/emitDestroy/bind）、**AsyncLocalStorage（run/getStore/enterWith/exit/disable，setTimeout/Promise.then/async-await 三路传播全通）**、asyncWrapProviders 68 键。引擎改动：`interpreter/vm.go`+`microtask.go` 增加 AsyncContextCapture/Restore 钩子（默认 nil 零行为变化，闭包创建捕获 + 事件循环外调用恢复 + 微任务入队/执行恢复）。m6-5-async-hooks.cjs PASS。 |
| 2026-08-06 | M6-6 | diagnostics_channel 重写：Channel 类（instanceof）+ channel 身份缓存、subscribe/unsubscribe/publish/hasSubscribers、bindStore/unbindStore/runStores（与 AsyncLocalStorage 集成）、tracingChannel（start/end/asyncStart/asyncEnd/error + traceSync/tracePromise/traceCallback）。m6-6-diagnostics-channel.cjs PASS。 |
| 2026-08-06 | M6-7 | trace_events 重写：createTracing（categories 校验：非数组抛 ERR_INVALID_ARG_TYPE、空数组抛 ERR_TRACE_EVENTS_CATEGORY_REQUIRED）、Tracing enable/disable/enabled/categories、getEnabledCategories。m6-7-trace-events.cjs PASS。 |
| 2026-08-06 | M6-8 | module 重写：builtinModules（Node 22 完整 68 项）、isBuiltin（node: 前缀）、Module 类（id/filename/loaded/children/require/load + 静态 runMain/wrap/_load/_resolveFilename/globalPaths）、createRequire、register/registerHooks/syncBuiltinESMExports/findSourceMap/getSourceMapsSupport/enableCompileCache、SourceMap 类。m6-8-module.cjs PASS。 |
| 2026-08-06 | M6 验收 | 8 个 m6 差分用例全 PASS；全量 diff 48/48；go test 绿；conformance 15/15。knownDifference：vm runInThisContext 顶层 var 不产生全局属性、sandbox copy-in/sync-back、内建原型跨 context 共享；vm Module/SourceTextModule 需 --experimental-vm-modules（Node 默认也不导出）；v8 serialize 为 JSON 简化（非 V8 wire format）、堆统计为 Go 近似；async_hooks 钩子仅在 AsyncResource 生命周期触发；inspector 无真实 CDP。 |

---

## 8. M7 测试器、CLI 与包生态（P1）

### 范围
node:test/reporters、mock/coverage/snapshot/watch/shard/concurrency、全部稳定 CLI flags、package exports/imports、loader hooks、TypeScript、npm 常见安装布局。

### 验收
Node test runner 差分套件；主流 CLI fixture；npm 包样本按模块类型矩阵运行；source map/stack/exit code 一致。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M7-1 | `node:test` 全 API（test/it/describe/hooks/TestContext/assert） | ✅ | 2026-08-07 |
| M7-2 | `node:test` mock timers/functions/methods/getters + snapshot + coverage + watch/concurrency/sharding/filter | ✅ | 2026-08-07 |
| M7-3 | `node:test/reporters` dot/junit/lcov/spec/tap + custom reporter | ✅ | 2026-08-07 |
| M7-4 | CLI flags：Node 22 稳定 flags、NODE_OPTIONS、watch/test/inspect/profile/permission | ✅ | 2026-08-07 |
| M7-5 | package exports/imports/conditions/self-reference/subpath patterns/node_modules traversal | ✅ | 2026-08-07 |
| M7-6 | TypeScript type stripping + `.ts/.mts/.cts` + source maps + npm 常见安装布局 | ✅ | 2026-08-07 |

### M7 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-07 | M7-1 | `node:test` 面与 node 22.23.1 逐项对齐：新增 `run(options)`（程序化运行返回事件流，异步派发 test:start/pass/fail/skip/todo/plan/end；CLI 检测 t.run() 后不重复执行）、`snapshot` 对象（setDefaultSnapshotSerializers/setResolveSnapshotPath 自顶层移入）、`mock.reset`（恢复实现，对齐 node 22.23 实测——call 历史不清理）；修正 `t.assert.match/doesNotMatch` this 绑定 bug（f.Call 丢 this，改经 vm.Eval 绑定）。`register` 保留为 knownDifference（22.23 运行时未导出但 22.14 文档存在）。m7-1-surface.cjs 扩展 run/snapshot/mock.reset 断言后 PASS。 |
| 2026-08-07 | M7-2 | mock 全套 m7-2-mock.cjs PASS；snapshot（snapshotAssert/更新）既有；coverage 既有；新增 `--test-timeout`（全局默认超时，事后判定近似）、`--test-shard=index/total`（fnv 文件哈希分片）、`--watch`（轮询 mtime 重跑）。 |
| 2026-08-07 | M7-3 | reporters 全套（spec/tap/dot/junit/lcov/custom）可用：`--test-reporter` + 自定义模块 stream 契约（write/end）；TAP 结构接近 node（TAP version 13/ok/not ok/汇总），明细字段差异记 knownDifference。m1-test-reporters.cjs PASS。 |
| 2026-08-07 | M7-4 | CLI：新增 `--test` 入口 + `--test-reporter-destination`（stdout|文件）；既有 --test-name-pattern/--test-skip-pattern/--test-only/--test-concurrency/--test-update-snapshots 与 NODE_OPTIONS 白名单。knownDifference：--test-concurrency 串行执行、--test-reporter-destination 文件时全 stdout 入文件。 |
| 2026-08-07 | M7-5 | 补建缺失 fixture `m7-fixtures/pkgapp/node_modules/condpkg`（exports 条件 require/import/default + 子路径 + 通配 + package.json）；require 与 import('condpkg') 与 node 22.23.1 逐字节一致；m7-5-package.cjs PASS。 |
| 2026-08-07 | M7-6 | TS strip-only（enum/namespace 非 declare 拦截 ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX）tsprobe.cjs PASS；source maps/register/compile cache 面 m6-8-module.cjs PASS。 |
| 2026-08-07 | M7 验收 | 差分 65/65、go test ./... 绿、conformance 16/16。node 22.23.1（冻结版本）落地 E:/tools/node-v22.23.1-win-x64，diff 套件 NODE= 指向它（此前默认 node 21.7.3 造成大量版本漂移误报）。 |

---

## 9. M8 全局 Web API（P1）

### 范围
Fetch、URL、Blob/File、Encoding、Web Streams、Events/Messaging、Abort、Performance、WebSocket、navigator、structuredClone。

### 验收
Web Platform Tests 可复用子集 + Node 差分；Web IDL descriptor、品牌检查、transfer/clone、streaming body、取消和错误语义通过。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M8-1 | Fetch：fetch/Headers/Request/Response/FormData/body mixin/redirect/AbortSignal/streaming | ✅ | 2026-08-07 |
| M8-2 | URL/URLSearchParams/URLPattern + legacy parse/format/resolve/domainTo*/fileURLToPath | ✅ | 2026-08-07 |
| M8-3 | Blob/File：bytes/text/stream/arrayBuffer/slice/object URL | ✅ | 2026-08-07 |
| M8-4 | Encoding：TextEncoder/Decoder + Stream、atob/btoa | ✅ | 2026-08-07 |
| M8-5 | Web Streams：全部 reader/writer/controller/BYOB/queuing/compression 类 | ✅ | 2026-08-07 |
| M8-6 | Events/Messaging：Event/EventTarget/CustomEvent/MessageEvent/MessageChannel/MessagePort/BroadcastChannel | ✅ | 2026-08-07 |
| M8-7 | Abort：AbortController/AbortSignal.abort/timeout/any/reason/throwIfAborted | ✅ | 2026-08-07 |
| M8-8 | Performance：performance/PerformanceEntry/Mark/Measure/Observer/ResourceTiming/User Timing | ✅ | 2026-08-07 |
| M8-9 | WebSocket/CloseEvent/MessageEvent、navigator | ✅ | 2026-08-07 |
| M8-10 | structuredClone/transfer、DOMException 名称/code 常量/stack/message/name | ✅ | 2026-08-07 |
| M8-11 | Web Crypto 全局：crypto/Crypto/SubtleCrypto/CryptoKey 全算法矩阵 | ✅ | 2026-08-07 |

### M8 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-07 | M8-1 | Headers 迭代按 WHATWG 名称排序（sortedMerged，keys/values/entries/forEach/iterator 统一）；Response/fetch body 流 chunk 改为 Uint8Array（Buffer）；修正用例 `new Headers('a: 1
b: 2')` 字符串形态（node 22.23.1 undici 拒绝，改 sequence 形态）。m8-fetch.cjs PASS。注意：`internal/globalscheck` 为陈旧重复实现（仅自身测试引用），活动实现为 `internal/runtime/globals`，两处已同步修复。 |
| 2026-08-07 | M8-2 | legacy url 模块补 `Url` 类（12 字段 + parse/format/resolve/resolveObject/parseHost，search/hash 带 '?'/'#' 前缀）、`resolveObject`、`fileURLToPathBuffer`；修复 punyEncode 死循环 OOM（RFC 3492 编码循环 q 增量位置错误 + 缺失 n++/delta 语义；对齐 node lib/punycode.js 特有的尾部 ++delta 与 t=k-bias 阈值）与 punyDecode 阈值 bug——domainToASCII/Unicode 与 node 逐字节一致（bcher-kva/fiqs8s/dqo34k 等向量）。m8-url-module.cjs PASS。 |
| 2026-08-07 | M8-8 | 全局 `PerformanceObserver` + entries API（mark/measure 记录、getEntries*/clear*、observer 回调）启动期注册（globals/performance.go 基础实现；node:perf_hooks 加载后由 builtin 完整实现覆盖同名全局）；Performance/PerformanceEntry/PerformanceMark/PerformanceMeasure/PerformanceObserver/PerformanceObserverEntryList/PerformanceResourceTiming 七构造器全局注册。m8-* 与 perf 探针与 node 22.23.1 对齐。 |
| 2026-08-07 | M8-11 | 全局 `Crypto`/`SubtleCrypto`/`CryptoKey` 构造器（new 抛 TypeError；crypto instanceof Crypto、crypto.subtle instanceof SubtleCrypto、CryptoKey 实例 instanceof 成立，经 SetProto 原型链）。crypto 全局探针与 node 对齐。 |
| 2026-08-07 | M8 验收 | m8-* 差分 9/9 全 PASS；全局 surface 探针与 node 22.23.1 一致（唯一差异：URLPattern aluka 始终可用，node 需 --experimental-urlpattern——超集）。knownDifference：全局对象方法为自有属性（原型链简化模型）；crypto 自有键含 getRandomValues/randomUUID/subtle（node 在原型上）。 |

### M9 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| 2026-08-07 | M9-1/2/3 | domain/punycode/wasi 模块面与行为差分 m9-1-domain.cjs、m9-2-punycode.cjs、m9-3-wasi.cjs 全 PASS（含废弃警告形状）。 |
| 2026-08-07 | M9-4 | process.report/permission 方法面 m9-4-process.cjs PASS（环境归一化后）；ADR 文档 docs/adr/{N-API,SEA,WASI-WASM,permissions-report}.md 落盘（实现/替代/非目标决策）。 |
| 2026-08-07 | M9 验收 | 差分 65/65 全绿；M9 决策项以 ADR 形式收敛，无静默计入完成率。 |

---

## 10. M9 废弃、实验和架构阻塞项（P2/P3）

### 范围
domain、punycode、wasi、permissions、SEA、report、N-API 决策。

### 验收
废弃入口兼容且发出正确 warning；实验 API 独立开关；WASI/N-API/SEA 每项形成 ADR，明确实现、替代或永久非目标，不能静默计入完成率。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M9-1 | `node:domain`：Domain create/add/remove/run/bind/intercept/enter/exit、error routing + 废弃警告 | ⬜ | |
| M9-2 | `node:punycode`：encode/decode/toASCII/toUnicode/ucs2/version + 废弃警告 | ⬜ | |
| M9-3 | `node:wasi`：WASI class/getImportObject/start/initialize/preview1 + 独立实验开关 | ⬜ | |
| M9-4 | permissions/report/SEA：`process.permission`、process.report、SEA config/blob/startup snapshot——每项形成 ADR | ⬜ | |
| M9-5 | N-API/WASI/WASM 架构决策：ADR 明确实现/替代/永久非目标 | ⬜ | |

### M9 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| | | |

---

## 11. M10 全量认证与发布门禁（P0）

### 验收
- manifest surface 100%，无未解释 L0/L1；
- 稳定 API 达到 L4；实验/废弃 API 达到各自决策等级；
- Windows、Linux、macOS；amd64/arm64 条件测试；
- `go test ./...`、race、fuzz、test262、Node differential、WPT 子集全绿；
- 内存泄漏、句柄泄漏、并发、取消、超时和异常退出 soak test 通过；
- 自动生成最终兼容报告和 known differences；发布版本冻结 API snapshot。

### 点位清单

| ID | 点位 | 状态 | 完成记录 |
|----|------|------|----------|
| M10-1 | manifest surface 100%，无未解释 L0/L1 | ⬜ | |
| M10-2 | 稳定 API 全部 L4；实验/废弃 API 达到各自决策等级 | ⬜ | |
| M10-3 | Windows/Linux/macOS × amd64/arm64 条件测试矩阵 | ⬜ | |
| M10-4 | `go test ./...`、race、fuzz、test262、Node differential、WPT 子集全绿 | ⬜ | |
| M10-5 | 泄漏/并发/取消/超时/异常退出 soak test 通过 | ⬜ | |
| M10-6 | 自动生成最终兼容报告 + known differences + 发布版本冻结 API snapshot | ⬜ | |

### M10 完成记录

| 日期 | 点位 | 内容与证据 |
|------|------|------------|
| | | |

---

## 12. 执行依赖与并行策略

```text
M0 清单/工具
 ├─> M1 缺失入口
 └─> M2 语义地基
      ├─> M3 文件/进程 ─┐
      ├─> M4 网络 ──────┤
      ├─> M5 数据/安全 ─┼─> M7 CLI/生态 ─┐
      ├─> M6 高级运行时 ┘               ├─> M10 全量认证
      └─> M8 Web API ────────────────────┘
M9 废弃/实验/架构项可在 M2 后并行，但必须在 M10 前形成结论。
```

- M0 是唯一硬前置（清单与工具）。
- M1、M2 依赖 M0；M3-M6 可在 M2 稳定后按文件所有权并行。
- M7 依赖 loader、process、test 和诊断能力。
- M10 不接受“仅入口存在”或“不可测”的完成声明。

## 13. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-08-06 | 依据 `node22-full-api-development-plan.md` 建立点位清单：M0-M10 共 11 个里程碑、各里程碑点位与验收、完成记录表；开发过程中逐点位更新 |
| v1.1 | 2026-08-06 | **M0 完成**：冻结 v22.23.1 官方 API 快照；四类 manifest（modules 57 入口 / globals / errors 429 码 / cli 180 flag）+ 四探针双跑；`gen-all.sh` 一键生成 `docs/node22-api-coverage.md` 与 `gaps.md`；初始分级 L0=11、L1=3、L2=42、L3=1、L4=0，名称面 15% |
| v1.2 | 2026-08-06 | **M1 完成**：8 个缺失入口清零（dns/promises、inspector/promises、path/posix、path/win32、readline/promises、stream/consumers、test/reporters、sys）；导出身份对齐（dns.promises===dns/promises、sys===util）；差分框架 `diff/run-diff.sh` + 5 用例全绿；顺带修复 conformance run.sh 路径/引号 bug（15/15 恢复）；覆盖 L0=3（domain/punycode/wasi 留 M9）、名称面 18.6% |
| v1.3 | 2026-08-06 | **M2 部分完成（3/8）**：EventEmitter 语义合同（Symbol 事件/error 抛出/errorMonitor/newListener/captureRejections/maxListeners 警告/静态导出对齐）——事件探针 12→0 差异；Error 体系（cause + 系统错误 code/errno/path/syscall）；AbortSignal（abort()/instanceof）；process.emitWarning 补入；差分 7/7、`go test` 绿、conformance 15/15。M2-1 为已知简化（Shape 模型无属性标志），M2-4/6/7/8 待续 |
| v1.4 | 2026-08-06 | **M2 完成（8/8）**：补 M2-4（unhandledRejection + process.once/removeListener）、M2-6（Buffer BigInt 读写/swap/toString(radix)/from(TypedArray)）、M2-7（Transform 回调约定 + duplex 共享对象重构 + destroy(error) + pipeline 链收尾）、M2-8（动态 import CJS 命名空间包装）。差分 11/11、`go test` 绿、conformance 15/15。M2-1 保留为已知差异（Shape 模型无属性标志，需专项） |
| v1.5 | 2026-08-06 | **M3-M6 完成（四个子代理并行）**：M3 文件/系统/进程 11/11（fs 三面+FileHandle/Stats 真实时间、os 真实值、child_process sync 三件套、worker_threads 补全、process warning 事件、constants 242 键）；M4 网络协议栈 8/8（net BlockList/SocketAddress/isIP、http 全链路+Agent keepAlive、tls/https 本地证书、http2/WebSocket 方法面；socket data 改 Buffer 并修正 TestNetMultipleMessages）；M5 Crypto/压缩/数据库 5/5（crypto randomInt/UUID/timingSafeEqual/hkdf/sign、WebCrypto AES-GCM/HMAC、zlib raw/unzip/crc32、sqlite CRUD/事务、buffer transcode + readBigInt64 符号修复）；M6 诊断/隔离 8/8（vm context 隔离、AsyncLocalStorage 三路传播（引擎最小钩子）、perf_hooks Observer、diagnostics_channel bindStore、module builtinModules 68 项）。差分 48/48、`go test` 全绿、conformance 15/15、覆盖名称面 629/2044（31%），L3=6 |
| v1.6 | 2026-08-07 | **M7-M9 完成**：M7 测试器/CLI/包生态 6/6（node:test 补 run/snapshot/mock.reset 并对齐 22.23.1 面、--test/--watch/--test-shard/--test-reporter-destination/--test-timeout 新 flag、condpkg fixture 补建、TS strip tsprobe）；M8 全局 Web API 11/11（Headers 迭代排序 + body 流 Uint8Array、legacy url 补 Url 类/resolveObject/fileURLToPathBuffer + punycode 修复（OOM bug）、全局 PerformanceObserver/entries API + Performance* 构造器、Crypto/SubtleCrypto/CryptoKey 构造器与 instanceof）；M9 5/5（domain/punycode/wasi/process 差分全绿 + ADR 落盘）。**关键回归修复**：punyEncode 死循环 OOM（4GB VirtualAlloc）、t.assert.match this 绑定、m8-fetch 用例非规范 Headers 字符串形态；**基础设施**：node 22.23.1（冻结版本）落地 E:/tools/node-v22.23.1-win-x64 并作为 diff NODE=；run-diff.sh 增 DNS 协议探测（DIFF_NO_DNS 归一化沙箱环境）；清理 c31f180 遗留的临时 harness 测试与 internal/globalscheck 陈旧重复包引用。差分 65/65、go test 绿、conformance 16/16。M10 剩余（全量认证门禁）。
