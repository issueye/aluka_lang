# Node 22 兼容缺口清单（M0 探针实测）

> 自动生成于 2026-08-07T02:33:06.645Z，对应冻结快照 v1.0。
> 缺口 = aluka 探针相对官方 manifest 缺失的条目；L 级判定见覆盖报告。

## 1. 缺失模块（加载失败 → L0，对应 M1）

共 0 个：。

## 2. 已有模块的名称面缺口（对应 M2-M6 逐模块审计）

| 模块 | 缺口数/总数 | 缺口样例 |
|------|-------------|----------|
| `assert` | 11/27 | assert、notDeepEqual、notDeepStrictEqual、assert.AssertionError、assert.Assert、assert.CallTracker、assert.CallTracker#calls、assert.CallTracker#getCalls |
| `assert/strict` | 11/27 | assert、notDeepEqual、notDeepStrictEqual、assert.AssertionError、assert.Assert、assert.CallTracker、assert.CallTracker#calls、assert.CallTracker#getCalls |
| `async_hooks` | 7/10 | AsyncHook、AsyncHook#enable、AsyncHook#disable、AsyncHook#executionAsyncResource、AsyncHook#executionAsyncId、AsyncHook#triggerAsyncId、AsyncHook#return |
| `buffer` | 78/83 | atob、btoa、resolveObjectURL、Blob、Blob#arrayBuffer、Blob#bytes、Blob#slice、Blob#stream |
| `child_process` | 14/21 | ChildProcess、ChildProcess#disconnect、ChildProcess#kill、ChildProcess#[Symbol.dispose]、ChildProcess#ref、ChildProcess#send、ChildProcess#unref、ChildProcess#Type |
| `cluster` | 17/22 | exit、listening、message、online、setup、Worker#disconnect、Worker#isConnected、Worker#isDead |
| `console` | 23/23 | profile、profileEnd、timeStamp、Console、Console#assert、Console#clear、Console#count、Console#countReset |
| `crypto` | 81/110 | createDiffieHellman、createDiffieHellmanGroup、createECDH、diffieHellman、generateKey、generateKeyPair、generateKeySync、generatePrime |
| `dgram` | 30/32 | dgram.Socket#addMembership、dgram.Socket#addSourceSpecificMembership、dgram.Socket#address、dgram.Socket#bind、dgram.Socket#close、dgram.Socket#[Symbol.asyncDispose]、dgram.Socket#connect、dgram.Socket#disconnect |
| `diagnostics_channel` | 13/25 | start、end、asyncStart、asyncEnd、error、Channel#return、TracingChannel、TracingChannel#subscribe |
| `dns` | 3/27 | cancel、dns.Resolver#Resolver、dns.Resolver#setLocalAddress |
| `dns/promises` | 1/23 | cancel |
| `domain` | 8/10 | Domain#add、Domain#bind、Domain#enter、Domain#exit、Domain#intercept、Domain#remove、Domain#run、Domain#Type |
| `events` | 27/53 | EventEmitter#event:newListener、events.EventEmitterAsyncResource#Type、Event、Event#composedPath、Event#initEvent、Event#preventDefault、Event#stopImmediatePropagation、Event#stopPropagation |
| `fs` | 68/169 | native、FileHandle#appendFile、FileHandle#chmod、FileHandle#chown、FileHandle#close、FileHandle#createReadStream、FileHandle#createWriteStream、FileHandle#datasync |
| `fs/promises` | 30/54 | cp、glob、lchmod、lchown、lutimes、rmdir、watch、FileHandle |
| `http` | 103/111 | setMaxIdleHTTPParsers、http.Agent#createConnection、http.Agent#keepSocketAlive、http.Agent#reuseSocket、http.Agent#destroy、http.Agent#getName、http.Agent#Type、http.ClientRequest |
| `http2` | 108/114 | performServerHandshake、Http2Session、Http2Session#close、Http2Session#destroy、Http2Session#goaway、Http2Session#ping、Http2Session#ref、Http2Session#setLocalWindowSize |
| `https` | 7/12 | https.Server#close、https.Server#[Symbol.asyncDispose]、https.Server#closeAllConnections、https.Server#closeIdleConnections、https.Server#listen、https.Server#setTimeout、https.Server#Type |
| `inspector` | 9/18 | dataReceived、dataSent、requestWillBeSent、responseReceived、loadingFinished、loadingFailed、inspector.Session#event:inspectorNotification、inspector.Session#event:<inspector-protocol-method>` |
| `inspector/promises` | 2/7 | inspector.Session#event:inspectorNotification、inspector.Session#event:<inspector-protocol-method>` |
| `module` | 3/17 | module.SourceMap#findEntry、module.SourceMap#findOrigin、module.SourceMap#return |
| `net` | 57/65 | getDefaultAutoSelectFamily、setDefaultAutoSelectFamily、getDefaultAutoSelectFamilyAttemptTimeout、setDefaultAutoSelectFamilyAttemptTimeout、net.BlockList#addAddress、net.BlockList#addRange、net.BlockList#addSubnet、net.BlockList#check |
| `perf_hooks` | 26/37 | PerformanceEntry#Type、PerformanceMark#Type、PerformanceMeasure#Type、PerformanceNodeEntry、PerformanceNodeEntry#Type、PerformanceNodeTiming、PerformanceNodeTiming#Type、PerformanceNodeTiming#return |
| `process` | 89/102 | availableMemory、constrainedMemory、disconnect、dlopen、execve、register、registerBeforeExit、unregister |
| `querystring` | 2/6 | decode、encode |
| `readline` | 32/38 | InterfaceConstructor、InterfaceConstructor#close、InterfaceConstructor#[Symbol.dispose]、InterfaceConstructor#pause、InterfaceConstructor#prompt、InterfaceConstructor#resume、InterfaceConstructor#setPrompt、InterfaceConstructor#getPrompt |
| `readline/promises` | 1/10 | readlinePromises.Interface#question |
| `repl` | 7/8 | REPLServer、REPLServer#defineCommand、REPLServer#displayPrompt、REPLServer#clearBufferedCommand、REPLServer#setupHistory、REPLServer#event:exit、REPLServer#event:reset |
| `sqlite` | 28/29 | backup、DatabaseSync#aggregate、DatabaseSync#close、DatabaseSync#loadExtension、DatabaseSync#enableLoadExtension、DatabaseSync#location、DatabaseSync#exec、DatabaseSync#function |
| `stream` | 13/15 | compose、isErrored、isReadable、isWritable、from、fromWeb、isDisturbed、toWeb |
| `stream/promises` | 13/15 | compose、isErrored、isReadable、isWritable、from、fromWeb、isDisturbed、toWeb |
| `stream/web` | 68/71 | from、arrayBuffer、blob、buffer、json、text、ReadableStream#cancel、ReadableStream#getReader |
| `string_decoder` | 2/3 | StringDecoder#end、StringDecoder#write |
| `sys` | 50/61 | debug、diff、getCallSites、getSystemErrorName、getSystemErrorMap、getSystemErrorMessage、setTraceSigInt、parseEnv |
| `test` | 70/83 | setDefaultSnapshotSerializers、setResolveSnapshotPath、MockFunctionContext、MockFunctionContext#callCount、MockFunctionContext#mockImplementation、MockFunctionContext#mockImplementationOnce、MockFunctionContext#resetCalls、MockFunctionContext#restore |
| `timers` | 15/21 | wait、yield、Immediate、Immediate#hasRef、Immediate#ref、Immediate#unref、Immediate#[Symbol.dispose]、Timeout |
| `timers/promises` | 2/5 | wait、yield |
| `tls` | 48/54 | createSecurePair、setDefaultCACertificates、getCACertificates、tls.SecurePair、tls.SecurePair#event:secure、tls.Server、tls.Server#addContext、tls.Server#address |
| `trace_events` | 2/4 | disable、enable |
| `tty` | 5/17 | tty.ReadStream#isRaw、tty.ReadStream#isTTY、tty.WriteStream#columns、tty.WriteStream#rows、tty.WriteStream#event:resize |
| `url` | 23/32 | URL、URL#toString、URL#toJSON、URL#createObjectURL、URL#revokeObjectURL、URL#canParse、URL#parse、URL#Type |
| `util` | 50/61 | debug、diff、getCallSites、getSystemErrorName、getSystemErrorMap、getSystemErrorMessage、setTraceSigInt、parseEnv |
| `util/types` | 48/61 | callbackify、debuglog、debug、deprecate、diff、format、formatWithOptions、getCallSites |
| `v8` | 18/56 | onInit、onSettled、onBefore、onAfter、createHook、init、before、after |
| `vm` | 13/25 | vm.Script#Type、vm.Module、vm.Module#evaluate、vm.Module#link、vm.Module#Type、vm.SourceTextModule、vm.SourceTextModule#createCachedData、vm.SourceTextModule#instantiate |
| `wasi` | 4/5 | WASI#getImportObject、WASI#start、WASI#initialize、WASI#Type |
| `worker_threads` | 31/43 | BroadcastChannel#close、BroadcastChannel#postMessage、BroadcastChannel#ref、BroadcastChannel#unref、BroadcastChannel#Type、MessagePort#close、MessagePort#postMessage、MessagePort#hasRef |
| `zlib` | 29/52 | createBrotliCompress、createBrotliDecompress、createDeflate、createDeflateRaw、createGunzip、createGzip、createInflate、createInflateRaw |

## 3. 全局对象缺口

- `localstorage`（global）
- `sessionstorage`（global）
- `CloseEvent`（global）
- `scheduler`（global）
- `EventSource`（class）
- `Navigator`（class）
- `PerformanceEntry`（class）
- `PerformanceMark`（class）
- `PerformanceMeasure`（class）
- `PerformanceObserverEntryList`（class）
- `PerformanceResourceTiming`（class）
- `ReadableStreamBYOBRequest`（class）
- `Storage`（class）
- `WebAssembly`（class）
- `Process`（class）

## 4. 事件语义缺口（EventEmitter 合同差异样例）

（无）
