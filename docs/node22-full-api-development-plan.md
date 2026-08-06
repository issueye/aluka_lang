# Node.js 22 及以前完整公开 API 兼容开发计划

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 日期：2026-08-06
> 基准：Node.js v22.23.1 本机运行时 + v22.23.2 官方 API JSON
> 目标范围：Node.js 22.x 及以前进入公开文档的 JavaScript API
> 关联文档：`docs/node22-compat-plan.md`（当前常用面）、
> `docs/development-plan.md`（运行时主计划）

## 1. 目标与验收口径

现有 Node 22 差分测试 15/15 只覆盖精选场景，不能推出“Node API 全兼容”。
本计划把兼容目标改为可枚举、可测试的完整公开面：

1. 所有公开 `node:` 内置模块入口及子路径。
2. 模块导出的函数、类、实例方法、属性、事件、常量和 Symbol 协议。
3. Node 全局对象、Web API、CJS/ESM 加载、包解析、CLI 与环境行为。
4. 参数校验、错误类型/错误码、异步时序、资源生命周期和跨平台差异。
5. 已废弃和实验 API 仍列入清单，但采用独立优先级和验收策略。

### 1.1 完成等级

| 等级 | 定义 | 可否标记完成 |
|------|------|--------------|
| L0 | 模块无法加载或全局不存在 | 否 |
| L1 | 入口和名称存在，可能为空桩 | 否 |
| L2 | 主流程可用，缺少边界、事件或错误语义 | 否 |
| L3 | 官方示例和模块级差分通过 | 阶段完成 |
| L4 | API manifest 100%、语义/错误/时序/资源测试全通过 | 全量完成 |

“已注册”“有同名方法”“精选用例通过”最多只能证明 L1/L2。只有 L4 才能在
README 中声明对应 API 完整兼容。

### 1.2 明确边界

| 范围 | 决策 |
|------|------|
| 下划线开头的内部模块，如 `_http_*`、`_stream_*` | 不作为公开兼容合同 |
| `domain`、`punycode`、`sys` 等废弃 API | 保留兼容，优先级 P3 |
| `wasi`、`sqlite`、permissions、SEA 等实验 API | 单列 P2/P3，不冒充稳定 API |
| C++ Addons、Node-API/N-API、V8 Embedder ABI | 列入清单但标为架构阻塞；纯 Go 无法二进制兼容 |
| Inspector CDP、V8 heap snapshot 格式 | 需要协议/格式级实现，不能以空 Session 代替 |
| 操作系统专属 API | Windows/Linux/macOS 分平台验收，不以单平台结果替代 |

### 1.3 数据源与复现

- 官方文档：<https://nodejs.org/docs/latest-v22.x/api/>
- 机器可读 API：<https://nodejs.org/docs/latest-v22.x/api/all.json>
- 模块入口：`require('node:module').builtinModules`，并补充其中未列出的
  `node:test`、`node:test/reporters`、`node:sqlite`。
- Aluka 当前入口：`internal/builtin/registry.go` 中的 `RegisterBuiltin`。

计划执行时必须把 Node patch 版本、官方 JSON 内容哈希、操作系统和架构写入
manifest；`latest-v22.x` 只能用于发现更新，正式验收使用冻结快照。

## 2. 当前基线

### 2.1 模块入口

Node 22 公开入口共 57 个；Aluka 注册 46 个，入口交集 46 个，缺失 11 个。
入口覆盖率为 80.7%，但 API 语义覆盖率尚未建立，不能沿用该比例。

缺失入口：

`dns/promises`、`domain`、`inspector/promises`、`path/posix`、`path/win32`、
`punycode`、`readline/promises`、`stream/consumers`、`sys`、`wasi`、
`test/reporters`。

已知仅覆盖最小面或存在显式留白：`vm`、`inspector`、`trace_events`、
`cluster`、`http2`、`stream/web`、`worker_threads`、`v8`、Web Crypto。

### 2.2 现有验收的真实含义

| 项目 | 当前结果 | 结论 |
|------|----------|------|
| Node 22 差分 | 15/15 | 精选场景通过，不代表完整 API |
| 注册模块 | 46/57 | 入口存在性，不代表导出完整 |
| `go test ./...` | 通过 | 项目回归门禁，不是 Node 语义认证 |
| 官方 API manifest | 尚无 | M0 必须首先建立 |
| 错误码/事件/资源矩阵 | 尚无 | 当前最大验收缺口 |

## 3. 完整模块清单

状态说明：`部分`表示已有入口但必须按 manifest 重新审计；`缺失`表示入口不存在。
下表的 API 面是该模块必须覆盖的公开类别，不以当前实现为准。

### 3.1 断言、工具与基础设施

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:assert` | 部分 | `Assert`、`AssertionError`、`CallTracker`；ok/fail/equal/deepEqual/strictEqual/deepStrictEqual/not*、throws/doesNotThrow/rejects/doesNotReject、ifError、match/doesNotMatch、partialDeepStrictEqual、strict |
| `node:assert/strict` | 部分 | 与 assert strict 导出身份、类和全部严格比较语义一致 |
| `node:util` | 部分 | callbackify、debuglog、deprecate、format/formatWithOptions、inspect、inherits、promisify、parseArgs、parseEnv、styleText、isDeepStrictEqual、MIMEType/MIMEParams、TextEncoder/TextDecoder、transferable abort API、system error API |
| `node:util/types` | 部分 | 所有 `is*` 类型谓词：ArrayBuffer/TypedArray/Map/Set/Iterator/Promise/Proxy/KeyObject/CryptoKey/boxed primitive 等 |
| `node:constants` | 部分 | fs、os、crypto、TLS、signal、priority、errno 的平台相关常量 |
| `node:module` | 部分 | Module/SourceMap、builtinModules、createRequire、isBuiltin、register/registerHooks、compile cache、source map、TypeScript strip、CJS 内部可观察属性 |
| `node:console` | 部分 | Console 类、全局 console 方法、分组/计数/计时/table/dir/trace/profile、格式化和流错误处理 |
| `node:querystring` | 部分 | decode/encode/escape/parse/stringify/unescape/unescapeBuffer、maxKeys 和自定义分隔符 |
| `node:string_decoder` | 部分 | StringDecoder.write/end、encoding 别名、多字节截断状态 |

### 3.2 异步上下文、事件和诊断

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:events` | 部分 | EventEmitter/EventEmitterAsyncResource；on/once/emit/listeners/rawListeners、captureRejections、errorMonitor、events.on/once、abort、max listeners、async iterator |
| `node:async_hooks` | 部分 | createHook 生命周期、execution/trigger IDs、AsyncResource、AsyncLocalStorage run/enterWith/exit/getStore/disable/bind/snapshot |
| `node:diagnostics_channel` | 部分 | Channel、channel/hasSubscribers/subscribe/unsubscribe、bindStore/runStores、TracingChannel、trace callbacks |
| `node:perf_hooks` | 部分 | performance 全部 entry API、PerformanceObserver、resource timing、timerify、eventLoopUtilization、monitorEventLoopDelay、createHistogram/RecordableHistogram |
| `node:trace_events` | 部分 | createTracing/getEnabledCategories、Tracing.enable/disable/categories、真实 trace 输出 |
| `node:inspector` | 部分 | Session connect/disconnect/post、open/close/url/waitForDebugger、Network resources、CDP 错误和通知 |
| `node:inspector/promises` | 缺失 | Promise Session.post 与 inspector 共享状态 |

### 3.3 Buffer、文件系统和路径

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:buffer` | 部分 | Buffer/SlowBuffer、Blob/File、atob/btoa、isAscii/isUtf8/transcode；alloc/from/concat/compare/byteLength；全部 read/write 数值方法、BigInt、slice/subarray/copy/fill/search/swap/JSON/编码 |
| `node:fs` | 部分 | callback/sync 两套 access、open/close/read/write/readv/writev、stat/lstat/fstat/statfs、chmod/chown、copy/cp、mkdir/mkdtemp、readFile/writeFile/append、readdir/opendir、link/symlink/readlink/realpath、rename/rm/rmdir/unlink/truncate、utimes、watch/watchFile、glob；Stats/Dirent/Dir/ReadStream/WriteStream/FileHandle 行为 |
| `node:fs/promises` | 部分 | fs Promise 对应面、FileHandle 全实例方法、AsyncIterator watch/glob/opendir、AbortSignal |
| `node:path` | 部分 | resolve/normalize/isAbsolute/join/relative/toNamespacedPath、dirname/basename/extname、format/parse、matchesGlob、sep/delimiter、posix/win32 身份 |
| `node:path/posix` | 缺失 | POSIX path 完整独立入口 |
| `node:path/win32` | 缺失 | Win32 path 完整独立入口 |

### 3.4 进程、系统、终端和子进程

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:process` | 部分 | argv/execArgv/env、cwd/chdir、exit/exitCode/abort、nextTick、hrtime、memoryUsage/resourceUsage/cpuUsage、signals、stdio、uid/gid/groups/umask、warnings、uncaught/rejection 事件、report、permission、features/versions/release/config、getBuiltinModule |
| `node:os` | 部分 | arch/platform/type/release/version/machine、cpus/availableParallelism/loadavg、memory/uptime、networkInterfaces、userInfo/homedir/tmpdir/hostname、priority、constants/EOL/devNull |
| `node:child_process` | 部分 | ChildProcess、spawn/exec/execFile/fork 及 Sync 版本；stdio/IPC/signal/timeout/AbortSignal/shell/windowsVerbatimArguments；close/exit/disconnect/message/error 时序 |
| `node:cluster` | 部分 | primary/worker 模式、setupPrimary/fork/disconnect/workers/settings/schedulingPolicy；Worker IPC、listen 句柄共享和全部事件 |
| `node:worker_threads` | 部分 | Worker/MessagePort/MessageChannel/BroadcastChannel、workerData、environmentData、resourceLimits、transferList、receive/postMessageToThread、heap/cpu profile、事件和终止语义 |
| `node:tty` | 部分 | ReadStream/WriteStream、raw mode、颜色深度、窗口尺寸、cursor/clear/move、resize、isatty |
| `node:readline` | 部分 | Interface/ReadLine、createInterface、promises/async iterator、question、completer、history、cursor/clear helpers、keypress events |
| `node:readline/promises` | 缺失 | Promises Interface/ReadLine、question/commit/rollback |
| `node:repl` | 部分 | REPLServer/start、commands、writer/eval/completer、recoverable errors、history/editor/context |

### 3.5 网络与协议

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:dns` | 部分 | lookup/lookupService、resolve 全记录类型、reverse、Resolver、servers/default order、错误码、callback 参数形态 |
| `node:dns/promises` | 缺失 | Promise lookup/resolve/Resolver 与 dns 共享配置 |
| `node:net` | 部分 | Socket/Server/BlockList/SocketAddress、connect/listen、half-open/backpressure/timeout/ref、IPv4/IPv6、auto family selection、IPC pipes、全部事件 |
| `node:dgram` | 部分 | UDP4/UDP6 Socket、bind/send/connect、membership/source membership、broadcast/TTL/buffer sizes/queue、ref、全部事件和错误码 |
| `node:tls` | 部分 | TLSSocket/Server/SecureContext、connect/createServer、cert/key/CA/SNI/ALPN/session/PSK、authorization、OCSP/keylog/secure events、TLS constants/defaults |
| `node:http` | 部分 | Agent/ClientRequest/IncomingMessage/OutgoingMessage/Server/ServerResponse、request/get/createServer、headers/trailers/timeouts/upgrades/CONNECT、keep-alive/pooling、parser limits、WebSocket exports |
| `node:https` | 部分 | HTTPS Agent/Server/request/get/createServer、TLS option 透传、session reuse |
| `node:http2` | 部分 | client/server session、stream、request/response、settings/ping/goaway/push、secure server、compat API、flow control、constants、全部事件与错误码 |

### 3.6 Crypto、压缩和数据

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:crypto` | 部分 | Hash/Hmac、Cipher/Decipher、Sign/Verify、KeyObject/CryptoKey/X509、RSA/EC/DH、key generate/import/export、random、PBKDF2/HKDF/scrypt、prime、timingSafeEqual、FIPS/engine、Web Crypto 全算法矩阵 |
| `node:zlib` | 部分 | Deflate/Inflate/Gzip/Gunzip/Raw/Unzip、Brotli、Zstd；sync/callback/stream 三种面、flush/params/reset、dictionary/limits/constants/crc32 |
| `node:sqlite` | 部分 | DatabaseSync/StatementSync/Session/SQLTagStore、prepare/exec/function/aggregate/backup/session changeset、参数绑定、错误码、权限和生命周期 |

### 3.7 Stream 家族

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:stream` | 部分 | Stream/Readable/Writable/Duplex/Transform/PassThrough、pipeline/finished/compose/addAbortSignal、from/toWeb、iterator/map/filter/reduce 等 consumers、背压/cork/destroy/事件时序 |
| `node:stream/promises` | 部分 | pipeline/finished Promise、AbortSignal、cleanup/end 选项 |
| `node:stream/consumers` | 缺失 | arrayBuffer/blob/buffer/json/text |
| `node:stream/web` | 部分 | Readable/Writable/Transform streams、BYOB、controllers/readers/writers、queuing strategies、Text/Compression streams、pipe/cancel/abort/tee |

### 3.8 URL、计时器和其他稳定模块

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:url` | 部分 | URL/URLSearchParams、legacy Url parse/format/resolve、domainToASCII/Unicode、fileURLToPath/pathToFileURL、urlToHttpOptions、object URL |
| `node:timers` | 部分 | Timeout/Immediate、set/clear/ref/unref/refresh/hasRef、Symbol.dispose、callback 参数和事件循环顺序 |
| `node:timers/promises` | 部分 | setTimeout/setImmediate/setInterval async iterator、scheduler.wait/yield、AbortSignal/ref |

### 3.9 VM、V8、测试与实验/废弃入口

| 模块 | 状态 | 必须覆盖的 API 面 |
|------|------|-------------------|
| `node:vm` | 部分/占位 | Script、Context、compileFunction、runIn*、cachedData、Module/SourceTextModule/SyntheticModule、measureMemory、dynamic import、context isolation/security |
| `node:v8` | 部分 | heap/code/space/cpp statistics、heap snapshot、coverage、serialize/deserialize 与 Serializer 类、promise hooks、startup snapshot、queryObjects、flags |
| `node:test` | 部分 | test/it/describe、hooks、TestContext、assert、mock timers/functions/methods/getters、snapshot、coverage、watch/concurrency/sharding/filter、subtests、AbortSignal、TAP/spec lifecycle |
| `node:test/reporters` | 缺失 | dot/junit/lcov/spec/tap reporters、custom reporter stream contract |
| `node:wasi` | 缺失 | WASI class、getImportObject/start/initialize、preview1、fd/env/args/preopens |
| `node:domain` | 缺失/废弃 | Domain create/add/remove/run/bind/intercept/enter/exit、error routing |
| `node:punycode` | 缺失/废弃 | encode/decode/toASCII/toUnicode/ucs2/version |
| `node:sys` | 缺失/废弃 | `node:util` 兼容别名和废弃警告 |

## 4. 全局与非模块 API 清单

### 4.1 Node 全局和 Web API

| 类别 | 必须覆盖的 API |
|------|----------------|
| 基础全局 | global/globalThis、process、console、Buffer、__dirname/__filename、require/module/exports（CJS） |
| 计时与任务 | set/clearTimeout、set/clearInterval、set/clearImmediate、queueMicrotask、ref/unref、nextTick 顺序 |
| Abort | AbortController、AbortSignal.abort/timeout/any、reason、throwIfAborted |
| URL/编码 | URL、URLSearchParams、URLPattern、TextEncoder/Decoder、TextEncoder/DecoderStream、atob/btoa |
| Fetch | fetch、Headers、Request、Response、FormData、body mixin、redirect、AbortSignal、streaming、cookies/proxy/TLS 行为 |
| Blob/File | Blob/File、bytes/text/stream/arrayBuffer/slice、object URL |
| Web Crypto | crypto/Crypto/SubtleCrypto/CryptoKey；digest/encrypt/decrypt/sign/verify/derive/generate/import/export/wrap/unwrap 的算法矩阵 |
| Web Streams | 全部 stream、reader/writer/controller/BYOB/queuing/compression 类和 Symbol.asyncIterator |
| Events/Messaging | Event/EventTarget/CustomEvent/MessageEvent、MessageChannel/MessagePort、BroadcastChannel、structuredClone/transfer |
| Performance | performance、PerformanceEntry/Mark/Measure/Observer/ResourceTiming、User Timing |
| Networking | WebSocket/CloseEvent/MessageEvent、navigator |
| DOM 错误 | DOMException 名称、code 常量、stack/message/name |

### 4.2 加载器、包和运行时行为

| 类别 | 必须覆盖的合同 |
|------|----------------|
| CommonJS | resolution/cache/cycles、extensions、require.resolve、module fields、CJS named exports、builtins precedence |
| ESM | import/export、TLA、dynamic import、import.meta、JSON/Wasm attributes、CJS interop、loader hooks、URL cache |
| Packages | package.json type/main/exports/imports、conditions、self-reference、subpath patterns、node_modules traversal、symlinks |
| TypeScript | type stripping、`.ts/.mts/.cts`、source maps、unsupported syntax diagnostics |
| Event loop | timers/poll/check/close、nextTick vs microtask、I/O callbacks、beforeExit/exit、unhandled rejection |
| Errors | JS error class、Node `ERR_*` code、system errno、message shape、cause、stack/async stack |
| CLI/env | Node 22 稳定 flags、NODE_OPTIONS、dotenv/env-file、watch/test/inspect/profile/permission flags、signals/exit codes |
| Permissions | process.permission、fs/net/worker/child-process checks、inheritance和 CLI policy |
| Diagnostics | report、source maps、CPU/heap profile、trace events、inspector、coverage |
| SEA | single executable config/blob/assets/startup snapshot（实验，独立验收） |

### 4.3 原生接口清单

| 接口 | 状态与决策 |
|------|------------|
| Node-API/N-API (`.node` addons) | 架构阻塞；需独立 ABI 兼容层，不纳入纯 Go 主线完成率 |
| C++ Addons/V8 API | 不可直接兼容；记录为明确非目标 |
| C++ Embedder API | 不可直接兼容；Aluka 提供 Go Engine/Context API 作为替代 |
| WASM/WASI | 需要先引入或实现 WebAssembly runtime；在 M9 做 go/no-go 决策 |

## 5. 交付物与自动化清单

全量工作开始前必须建立以下机器可读资产：

| 产物 | 内容 |
|------|------|
| `tests/compat/node22/manifest/modules.json` | 57 个入口、导出、类、原型方法、属性、事件、常量 |
| `tests/compat/node22/manifest/globals.json` | Node 全局与 Web API surface |
| `tests/compat/node22/manifest/errors.json` | `ERR_*`、errno、错误类与参数条件 |
| `tests/compat/node22/manifest/cli.json` | CLI flags、环境变量、退出码和平台条件 |
| `tests/compat/node22/probe/` | Node/Aluka 双跑的 surface 与 descriptor 探针 |
| `tests/compat/node22/diff/` | 行为、错误、事件顺序、资源释放差分用例 |
| `docs/node22-api-coverage.md` | 自动生成覆盖表，不手工维护百分比 |

manifest 每项至少记录：`name`、`kind`、`module/global`、`added`、`stability`、
`platform`、`status`、`tests`、`knownDifference`。

## 6. 里程碑

### M0：官方清单与差分基础设施（P0）

目标：停止以人工表格和少量 smoke test 估算兼容率。

- 固定 Node v22.23.x 文档和运行时快照。
- 生成 modules/globals/errors/cli 四类 manifest。
- 建立 descriptor、导出身份、类/原型、Symbol、事件探针。
- 输出初始 L0-L4 覆盖报告和缺口 issue 列表。

验收：57/57 入口都有 manifest；官方 JSON 中稳定 JavaScript API 无未归属项；
coverage 文档可由命令重新生成。

### M1：缺失入口与别名/Promise 子路径（P0）

范围：`dns/promises`、`inspector/promises`、`path/posix`、`path/win32`、
`readline/promises`、`stream/consumers`、`test/reporters`，以及 `sys` 别名。

验收：缺失稳定入口清零；导出身份与 Node 一致；每个子路径至少包含 surface、
成功、失败、取消/资源释放四类差分。

### M2：运行时语义地基（P0）

范围：Value/Property descriptor、Error/ERR_*、EventEmitter、Promise/microtask、
AbortSignal、Buffer/TypedArray、Stream/backpressure、CJS/ESM loader。

验收：后续模块共用的错误、事件、异步时序和资源模型稳定；test262 与现有
conformance 不倒退；跨模块 contract 测试全绿。

### M3：文件、系统与进程（P0）

范围：fs/fs-promises、path、os、process、child_process、worker_threads、tty、
readline、repl、cluster。

验收：callback/sync/promise 三面一致；Windows/Linux 双平台；权限、signal、
stdio/IPC、watch、文件描述符生命周期和错误码差分通过。

### M4：网络协议栈（P0）

范围：dns、net、dgram、tls、http、https、http2、WebSocket。

验收：IPv4/IPv6、TLS/SNI/ALPN、代理、keep-alive/pooling、backpressure、timeout、
AbortSignal、upgrade/CONNECT、HTTP/2 session/flow-control 和资源泄漏长跑测试通过。

### M5：Crypto、压缩与数据库（P1）

范围：crypto/Web Crypto、zlib、sqlite、buffer 编码补全。

验收：算法/格式矩阵、同步/异步/stream 三面、Known Answer Tests、OpenSSL/Node
互操作、压缩 bomb 限制、SQLite transaction/session/binding 差分通过。

### M6：诊断、隔离与高级运行时（P1）

范围：vm、v8、inspector、perf_hooks、async_hooks、diagnostics_channel、
trace_events、module hooks/source maps。

验收：Context 真隔离；vm module 生命周期正确；CDP 基础命令可用；profile、
coverage、heap/trace 产物可被标准工具读取；AsyncLocalStorage 跨异步资源正确。

### M7：测试器、CLI 与包生态（P1）

范围：node:test/reporters、mock/coverage/snapshot/watch/shard/concurrency、全部稳定
CLI flags、package exports/imports、loader hooks、TypeScript、npm 常见安装布局。

验收：Node test runner 差分套件；主流 CLI fixture；npm 包样本按模块类型矩阵
运行；source map/stack/exit code 一致。

### M8：全局 Web API（P1）

范围：Fetch、URL、Blob/File、Encoding、Web Streams、Events/Messaging、Abort、
Performance、WebSocket、navigator、structuredClone。

验收：Web Platform Tests 可复用子集 + Node 差分；Web IDL descriptor、品牌检查、
transfer/clone、streaming body、取消和错误语义通过。

### M9：废弃、实验和架构阻塞项（P2/P3）

范围：domain、punycode、wasi、permissions、SEA、report、N-API 决策。

验收：废弃入口兼容且发出正确 warning；实验 API 独立开关；WASI/N-API/SEA 每项
形成 ADR，明确实现、替代或永久非目标，不能静默计入完成率。

### M10：全量认证与发布门禁（P0）

- manifest surface 100%，无未解释 L0/L1。
- 稳定 API 达到 L4；实验/废弃 API 达到各自决策等级。
- Windows、Linux、macOS；amd64/arm64 条件测试。
- `go test ./...`、race、fuzz、test262、Node differential、WPT 子集全绿。
- 内存泄漏、句柄泄漏、并发、取消、超时和异常退出 soak test 通过。
- 自动生成最终兼容报告和 known differences；发布版本冻结 API snapshot。

## 7. 依赖与执行顺序

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

M3-M6 可在 M2 稳定后按文件所有权并行；M7 依赖 loader、process、test 和诊断能力；
M10 不接受“仅入口存在”或“不可测”的完成声明。

## 8. 每个 API 的完成定义

单个 API 只有同时满足以下条件才能标记 L4：

1. 名称、property descriptor、函数 length/name、类继承和导出身份一致。
2. 所有签名、默认值、类型转换、overload 和 options 字段已测试。
3. 同步结果、Promise/callback 结果、事件名称和先后顺序一致。
4. 错误类、`code`、errno、参数名、cause 和关键 message 片段一致。
5. AbortSignal、timeout、ref/unref、close/destroy 和资源释放语义一致。
6. Windows/Linux/macOS 差异有条件测试或明确 known difference。
7. 至少一个成功、一个边界、一个失败和一个清理/取消差分用例。
8. 官方示例可运行；相关性能和内存无明显回退。

## 9. 风险与治理

| 风险 | 治理措施 |
|------|----------|
| API 数量大且 Node 22 patch 版本仍变化 | 冻结 v22.23.x snapshot；升级必须走 manifest diff |
| 入口存在被误判为兼容 | 报告只统计 L3/L4，L1 不计兼容率 |
| Go 标准库抽象与 Node/libuv 不同 | 用可观察语义验收，不照搬内部结构；无法对齐时 ADR |
| 网络/时间测试不稳定 | loopback、虚拟时钟、确定性证书和 bounded retry |
| 平台差异遗漏 | CI 三系统矩阵，platform 字段是 manifest 必填项 |
| Inspector/V8/WASI/N-API 夸大能力 | 协议/格式/ABI 级验收，明确架构边界 |
| 文档清单手工漂移 | 官方 JSON + runtime probe 自动生成，禁止手改覆盖率数字 |

## 10. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-08-06 | 建立 Node 22 及以前完整公开 API 范围：57 个模块入口、全局/loader/CLI/错误/原生边界清单，M0-M10 里程碑、依赖和 L0-L4 验收体系 |
