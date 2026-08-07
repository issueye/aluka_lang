# Aluka × Node 22 完整公开 API 覆盖报告

> 自动生成：`tests/compat/node22/gen-all.sh`（gen-manifest → run-probe → gen-coverage），禁止手工修改。
> 数据源：官方 API JSON v22.23.1（sha256 `ba180cb8908e1ff4247f4b71fe55042caddab8b2f4fcc2d80f28945d8d701bf1`）、本机 Node v22.23.1、Aluka 探针实测。
> 平台：windows/amd64 ｜ 生成时间：2026-08-07T02:33:06.643Z

## 1. 总体结论

- 入口：57/57 有 manifest；名称面覆盖 644/2044（32%）。
- 等级分布：L0=0，L1=2，L2=48，L3=7，L4=0。
- **L0-L4 判定口径**：本报告为探针初始分级（名称面近似）。L3 表示 manifest 名称面 100% 存在；L4 只在对应模块差分/语义测试通过后授予（M10 全量认证）。
- 已知差异与缺口见 [gaps.md](./node22/../tests/compat/node22/gaps.md) 与下文章节。

## 2. 模块清单

| 模块 | L 级 | 文档页 | 名称面 | 覆盖 | 缺口样例 |
|------|------|--------|--------|------|----------|
| `assert` | L2 | assert | 27 | 59% | assert、notDeepEqual、notDeepStrictEqual、assert.AssertionError、assert.Assert |
| `assert/strict` | L2 | assert | 27 | 59% | assert、notDeepEqual、notDeepStrictEqual、assert.AssertionError、assert.Assert |
| `async_hooks` | L2 | async_hooks | 10 | 30% | AsyncHook、AsyncHook#enable、AsyncHook#disable、AsyncHook#executionAsyncResource、AsyncHook#executionAsyncId |
| `buffer` | L2 | buffer | 83 | 6% | atob、btoa、resolveObjectURL、Blob、Blob#arrayBuffer |
| `child_process` | L2 | child_process | 21 | 33% | ChildProcess、ChildProcess#disconnect、ChildProcess#kill、ChildProcess#[Symbol.dispose]、ChildProcess#ref |
| `cluster` | L2 | cluster | 22 | 23% | exit、listening、message、online、setup |
| `console` | L1 | console | 23 | 0% | profile、profileEnd、timeStamp、Console、Console#assert |
| `constants` | L1 | os | 0 | - | - |
| `crypto` | L2 | crypto | 110 | 26% | createDiffieHellman、createDiffieHellmanGroup、createECDH、diffieHellman、generateKey |
| `dgram` | L2 | dgram | 32 | 6% | dgram.Socket#addMembership、dgram.Socket#addSourceSpecificMembership、dgram.Socket#address、dgram.Socket#bind、dgram.Socket#close |
| `diagnostics_channel` | L2 | diagnostics_channel | 25 | 48% | start、end、asyncStart、asyncEnd、error |
| `dns` | L2 | dns | 27 | 89% | cancel、dns.Resolver#Resolver、dns.Resolver#setLocalAddress |
| `dns/promises` | L2 | dns | 23 | 96% | cancel |
| `domain` | L2 | domain | 10 | 20% | Domain#add、Domain#bind、Domain#enter、Domain#exit、Domain#intercept |
| `events` | L2 | Events | 53 | 49% | EventEmitter#event:newListener、events.EventEmitterAsyncResource#Type、Event、Event#composedPath、Event#initEvent |
| `fs` | L2 | fs | 169 | 60% | native、FileHandle#appendFile、FileHandle#chmod、FileHandle#chown、FileHandle#close |
| `fs/promises` | L2 | fs | 54 | 44% | cp、glob、lchmod、lchown、lutimes |
| `http` | L2 | http | 111 | 7% | setMaxIdleHTTPParsers、http.Agent#createConnection、http.Agent#keepSocketAlive、http.Agent#reuseSocket、http.Agent#destroy |
| `http2` | L2 | http/2 | 114 | 5% | performServerHandshake、Http2Session、Http2Session#close、Http2Session#destroy、Http2Session#goaway |
| `https` | L2 | https | 12 | 42% | https.Server#close、https.Server#[Symbol.asyncDispose]、https.Server#closeAllConnections、https.Server#closeIdleConnections、https.Server#listen |
| `inspector` | L2 | inspector | 18 | 50% | dataReceived、dataSent、requestWillBeSent、responseReceived、loadingFinished |
| `inspector/promises` | L2 | inspector | 7 | 71% | inspector.Session#event:inspectorNotification、inspector.Session#event:<inspector-protocol-method>` |
| `module` | L2 | modules:_`node:module`_api | 17 | 82% | module.SourceMap#findEntry、module.SourceMap#findOrigin、module.SourceMap#return |
| `net` | L2 | net | 65 | 12% | getDefaultAutoSelectFamily、setDefaultAutoSelectFamily、getDefaultAutoSelectFamilyAttemptTimeout、setDefaultAutoSelectFamilyAttemptTimeout、net.BlockList#addAddress |
| `os` | L3 | os | 20 | 100% | - |
| `path` | L3 | path | 12 | 100% | - |
| `path/posix` | L3 | path | 12 | 100% | - |
| `path/win32` | L3 | path | 12 | 100% | - |
| `perf_hooks` | L2 | performance_measurement_apis | 37 | 30% | PerformanceEntry#Type、PerformanceMark#Type、PerformanceMeasure#Type、PerformanceNodeEntry、PerformanceNodeEntry#Type |
| `process` | L2 | global-objects | 102 | 13% | availableMemory、constrainedMemory、disconnect、dlopen、execve |
| `punycode` | L3 | punycode | 4 | 100% | - |
| `querystring` | L2 | querystring | 6 | 67% | decode、encode |
| `readline` | L2 | readline | 38 | 16% | InterfaceConstructor、InterfaceConstructor#close、InterfaceConstructor#[Symbol.dispose]、InterfaceConstructor#pause、InterfaceConstructor#prompt |
| `readline/promises` | L2 | readline | 10 | 90% | readlinePromises.Interface#question |
| `repl` | L2 | repl | 8 | 13% | REPLServer、REPLServer#defineCommand、REPLServer#displayPrompt、REPLServer#clearBufferedCommand、REPLServer#setupHistory |
| `sqlite` | L2 | sqlite | 29 | 3% | backup、DatabaseSync#aggregate、DatabaseSync#close、DatabaseSync#loadExtension、DatabaseSync#enableLoadExtension |
| `stream` | L2 | stream | 15 | 13% | compose、isErrored、isReadable、isWritable、from |
| `stream/consumers` | L3 | stream | 5 | 100% | - |
| `stream/promises` | L2 | stream | 15 | 13% | compose、isErrored、isReadable、isWritable、from |
| `stream/web` | L2 | web_streams_api | 71 | 4% | from、arrayBuffer、blob、buffer、json |
| `string_decoder` | L2 | string_decoder | 3 | 33% | StringDecoder#end、StringDecoder#write |
| `sys` | L2 | util | 61 | 18% | debug、diff、getCallSites、getSystemErrorName、getSystemErrorMap |
| `test` | L2 | test_runner | 83 | 16% | setDefaultSnapshotSerializers、setResolveSnapshotPath、MockFunctionContext、MockFunctionContext#callCount、MockFunctionContext#mockImplementation |
| `test/reporters` | L3 | test_runner | 5 | 100% | - |
| `timers` | L2 | timers | 21 | 29% | wait、yield、Immediate、Immediate#hasRef、Immediate#ref |
| `timers/promises` | L2 | timers | 5 | 60% | wait、yield |
| `tls` | L2 | tls_(ssl) | 54 | 11% | createSecurePair、setDefaultCACertificates、getCACertificates、tls.SecurePair、tls.SecurePair#event:secure |
| `trace_events` | L2 | trace_events | 4 | 50% | disable、enable |
| `tty` | L2 | tty | 17 | 71% | tty.ReadStream#isRaw、tty.ReadStream#isTTY、tty.WriteStream#columns、tty.WriteStream#rows、tty.WriteStream#event:resize |
| `url` | L2 | url | 32 | 28% | URL、URL#toString、URL#toJSON、URL#createObjectURL、URL#revokeObjectURL |
| `util` | L2 | util | 61 | 18% | debug、diff、getCallSites、getSystemErrorName、getSystemErrorMap |
| `util/types` | L2 | util | 61 | 21% | callbackify、debuglog、debug、deprecate、diff |
| `v8` | L2 | v8 | 56 | 68% | onInit、onSettled、onBefore、onAfter、createHook |
| `vm` | L2 | vm | 25 | 48% | vm.Script#Type、vm.Module、vm.Module#evaluate、vm.Module#link、vm.Module#Type |
| `wasi` | L2 | webassembly_system_interface_(wasi) | 5 | 20% | WASI#getImportObject、WASI#start、WASI#initialize、WASI#Type |
| `worker_threads` | L2 | worker_threads | 43 | 28% | BroadcastChannel#close、BroadcastChannel#postMessage、BroadcastChannel#ref、BroadcastChannel#unref、BroadcastChannel#Type |
| `zlib` | L2 | zlib | 52 | 44% | createBrotliCompress、createBrotliDecompress、createDeflate、createDeflateRaw、createGunzip |

## 3. 全局与 Web API

### 3.1 全局对象

| 全局 | 状态 |
|------|------|
| `__dirname` | ✅ |
| `__filename` | ✅ |
| `console` | ✅ |
| `crypto` | ✅ |
| `exports` | ✅ |
| `fetch` | ✅ |
| `global` | ✅ |
| `localstorage` | ❌ 缺失 |
| `module` | ✅ |
| `navigator` | ✅ |
| `performance` | ✅ |
| `process` | ✅ |
| `sessionstorage` | ❌ 缺失 |
| `globalThis` | ✅ |
| `URLPattern` | ✅ |
| `AbortSignal` | ✅ |
| `MessageEvent` | ✅ |
| `CloseEvent` | ❌ 缺失 |
| `scheduler` | ❌ 缺失 |

### 3.2 全局类

| 类 | 状态 |
|----|------|
| `AbortController` | ✅ |
| `Blob` | ✅ |
| `Buffer` | ✅ |
| `ByteLengthQueuingStrategy` | ✅ |
| `BroadcastChannel` | ✅ |
| `CompressionStream` | ✅ |
| `CountQueuingStrategy` | ✅ |
| `Crypto` | ✅ |
| `CryptoKey` | ✅ |
| `CustomEvent` | ✅ |
| `DecompressionStream` | ✅ |
| `Event` | ✅ |
| `EventSource` | ❌ 缺失 |
| `EventTarget` | ✅ |
| `File` | ✅ |
| `FormData` | ✅ |
| `Headers` | ✅ |
| `MessageChannel` | ✅ |
| `MessageEvent` | ✅ |
| `MessagePort` | ✅ |
| `Navigator` | ❌ 缺失 |
| `PerformanceEntry` | ❌ 缺失 |
| `PerformanceMark` | ❌ 缺失 |
| `PerformanceMeasure` | ❌ 缺失 |
| `PerformanceObserver` | ✅ |
| `PerformanceObserverEntryList` | ❌ 缺失 |
| `PerformanceResourceTiming` | ❌ 缺失 |
| `ReadableByteStreamController` | ✅ |
| `ReadableStream` | ✅ |
| `ReadableStreamBYOBReader` | ✅ |
| `ReadableStreamBYOBRequest` | ❌ 缺失 |
| `ReadableStreamDefaultController` | ✅ |
| `ReadableStreamDefaultReader` | ✅ |
| `Response` | ✅ |
| `Request` | ✅ |
| `Storage` | ❌ 缺失 |
| `SubtleCrypto` | ✅ |
| `DOMException` | ✅ |
| `TextDecoder` | ✅ |
| `TextDecoderStream` | ✅ |
| `TextEncoder` | ✅ |
| `TextEncoderStream` | ✅ |
| `TransformStream` | ✅ |
| `TransformStreamDefaultController` | ✅ |
| `URL` | ✅ |
| `URLSearchParams` | ✅ |
| `WebAssembly` | ❌ 缺失 |
| `WebSocket` | ✅ |
| `WritableStream` | ✅ |
| `WritableStreamDefaultController` | ✅ |
| `WritableStreamDefaultWriter` | ✅ |
| `Process` | ❌ 缺失 |

### 3.3 全局方法

| 方法 | 状态 |
|------|------|
| `atob` | ✅ |
| `btoa` | ✅ |
| `clearImmediate` | ✅ |
| `clearInterval` | ✅ |
| `clearTimeout` | ✅ |
| `queueMicrotask` | ✅ |
| `require` | ✅ |
| `setImmediate` | ✅ |
| `setInterval` | ✅ |
| `setTimeout` | ✅ |
| `structuredClone` | ✅ |

## 4. 事件语义探针差异（EventEmitter 合同）

（无差异或探针未生成）

## 5. 错误与 CLI 清单规模

- errors.json：429 个错误码（ERR_* 为主），7 个错误类。
- cli.json：180 个 CLI flags，29 个环境变量，249 个退出码项。

## 6. 再生成命令

```bash
cd tests/compat/node22
bash gen-all.sh            # 一键重建 manifest + 探针 + 本报告 + gaps.md
# 分步：
node tools/gen-manifest.mjs   # 1) 从 data/all.json 生成四类 manifest
bash run-probe.sh             # 2) node 与 aluka 双跑探针
node tools/gen-coverage.mjs   # 3) 生成覆盖报告与 gaps.md
```
