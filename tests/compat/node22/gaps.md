# Node 22 兼容缺口清单（M0 探针实测）

> 自动生成于 2026-08-06T14:02:17.202Z，对应冻结快照 v1.0。
> 缺口 = aluka 探针相对官方 manifest 缺失的条目；L 级判定见覆盖报告。

## 1. 缺失模块（加载失败 → L0，对应 M1）

共 3 个：`domain`、`punycode`、`wasi`。

## 2. 已有模块的名称面缺口（对应 M2-M6 逐模块审计）

| 模块 | 缺口数/总数 | 缺口样例 |
|------|-------------|----------|
| `assert` | 11/27 | assert、notDeepEqual、notDeepStrictEqual、assert.AssertionError、assert.Assert、assert.CallTracker、assert.CallTracker#calls、assert.CallTracker#getCalls |
| `assert/strict` | 11/27 | assert、notDeepEqual、notDeepStrictEqual、assert.AssertionError、assert.Assert、assert.CallTracker、assert.CallTracker#calls、assert.CallTracker#getCalls |
| `async_hooks` | 9/10 | createHook、AsyncHook、AsyncHook#enable、AsyncHook#disable、AsyncHook#executionAsyncResource、AsyncHook#executionAsyncId、AsyncHook#triggerAsyncId、AsyncHook#return |
| `buffer` | 79/83 | atob、btoa、resolveObjectURL、transcode、Blob、Blob#arrayBuffer、Blob#bytes、Blob#slice |
| `child_process` | 14/21 | ChildProcess、ChildProcess#disconnect、ChildProcess#kill、ChildProcess#[Symbol.dispose]、ChildProcess#ref、ChildProcess#send、ChildProcess#unref、ChildProcess#Type |
| `cluster` | 18/22 | setupPrimary、exit、listening、message、online、setup、Worker#disconnect、Worker#isConnected |
| `console` | 23/23 | profile、profileEnd、timeStamp、Console、Console#assert、Console#clear、Console#count、Console#countReset |
| `crypto` | 97/110 | checkPrime、checkPrimeSync、createDiffieHellman、createDiffieHellmanGroup、createECDH、createPublicKey、createSecretKey、createSign |
| `dgram` | 30/32 | dgram.Socket#addMembership、dgram.Socket#addSourceSpecificMembership、dgram.Socket#address、dgram.Socket#bind、dgram.Socket#close、dgram.Socket#[Symbol.asyncDispose]、dgram.Socket#connect、dgram.Socket#disconnect |
| `diagnostics_channel` | 20/25 | start、end、asyncStart、asyncEnd、error、Channel、Channel#publish、Channel#subscribe |
| `dns` | 22/27 | getServers、lookupService、resolveAny、resolveCname、resolveCaa、resolveMx、resolveNaptr、resolveNs |
| `dns/promises` | 1/23 | cancel |
| `domain` | 10/10 | create、Domain、Domain#add、Domain#bind、Domain#enter、Domain#exit、Domain#intercept、Domain#remove |
| `events` | 27/53 | EventEmitter#event:newListener、events.EventEmitterAsyncResource#Type、Event、Event#composedPath、Event#initEvent、Event#preventDefault、Event#stopImmediatePropagation、Event#stopPropagation |
| `fs` | 148/169 | access、appendFile、chmod、chown、copyFile、lchmod、lchown、lutimes |
| `fs/promises` | 39/54 | chmod、chown、cp、glob、lchmod、lchown、lutimes、link |
| `http` | 105/111 | validateHeaderName、validateHeaderValue、setMaxIdleHTTPParsers、http.Agent#createConnection、http.Agent#keepSocketAlive、http.Agent#reuseSocket、http.Agent#destroy、http.Agent#getName |
| `http2` | 105/114 | createSecureServer、performServerHandshake、Http2Session#close、Http2Session#destroy、Http2Session#goaway、Http2Session#ping、Http2Session#ref、Http2Session#setLocalWindowSize |
| `https` | 8/12 | https.Server、https.Server#close、https.Server#[Symbol.asyncDispose]、https.Server#closeAllConnections、https.Server#closeIdleConnections、https.Server#listen、https.Server#setTimeout、https.Server#Type |
| `inspector` | 9/18 | dataReceived、dataSent、requestWillBeSent、responseReceived、loadingFinished、loadingFailed、inspector.Session#event:inspectorNotification、inspector.Session#event:<inspector-protocol-method>` |
| `inspector/promises` | 2/7 | inspector.Session#event:inspectorNotification、inspector.Session#event:<inspector-protocol-method>` |
| `module` | 16/17 | enableCompileCache、getCompileCacheDir、findPackageJSON、isBuiltin、register、registerHooks、stripTypeScriptTypes、syncBuiltinESMExports |
| `net` | 62/65 | getDefaultAutoSelectFamily、setDefaultAutoSelectFamily、getDefaultAutoSelectFamilyAttemptTimeout、setDefaultAutoSelectFamilyAttemptTimeout、isIP、isIPv4、isIPv6、net.BlockList |
| `os` | 6/20 | availableParallelism、getPriority、loadavg、machine、setPriority、version |
| `perf_hooks` | 37/37 | createHistogram、monitorEventLoopDelay、PerformanceEntry、PerformanceEntry#Type、PerformanceMark、PerformanceMark#Type、PerformanceMeasure、PerformanceMeasure#Type |
| `process` | 90/102 | abort、availableMemory、constrainedMemory、disconnect、dlopen、execve、register、registerBeforeExit |
| `punycode` | 4/4 | decode、encode、toASCII、toUnicode |
| `querystring` | 2/6 | decode、encode |
| `readline` | 37/38 | emitKeypressEvents、clearLine、clearScreenDown、cursorTo、moveCursor、InterfaceConstructor、InterfaceConstructor#close、InterfaceConstructor#[Symbol.dispose] |
| `readline/promises` | 1/10 | readlinePromises.Interface#question |
| `repl` | 7/8 | REPLServer、REPLServer#defineCommand、REPLServer#displayPrompt、REPLServer#clearBufferedCommand、REPLServer#setupHistory、REPLServer#event:exit、REPLServer#event:reset |
| `sqlite` | 28/29 | backup、DatabaseSync#aggregate、DatabaseSync#close、DatabaseSync#loadExtension、DatabaseSync#enableLoadExtension、DatabaseSync#location、DatabaseSync#exec、DatabaseSync#function |
| `stream` | 13/15 | compose、isErrored、isReadable、isWritable、from、fromWeb、isDisturbed、toWeb |
| `stream/promises` | 13/15 | compose、isErrored、isReadable、isWritable、from、fromWeb、isDisturbed、toWeb |
| `stream/web` | 68/71 | from、arrayBuffer、blob、buffer、json、text、ReadableStream#cancel、ReadableStream#getReader |
| `string_decoder` | 2/3 | StringDecoder#end、StringDecoder#write |
| `sys` | 50/61 | debug、diff、getCallSites、getSystemErrorName、getSystemErrorMap、getSystemErrorMessage、setTraceSigInt、parseEnv |
| `test` | 76/83 | run、suite、skip、todo、only、register、setDefaultSnapshotSerializers、setResolveSnapshotPath |
| `timers` | 15/21 | wait、yield、Immediate、Immediate#hasRef、Immediate#ref、Immediate#unref、Immediate#[Symbol.dispose]、Timeout |
| `timers/promises` | 2/5 | wait、yield |
| `tls` | 51/54 | checkServerIdentity、createSecureContext、createSecurePair、setDefaultCACertificates、getCACertificates、getCiphers、tls.SecurePair、tls.SecurePair#event:secure |
| `trace_events` | 2/4 | disable、enable |
| `tty` | 14/17 | tty.ReadStream#setRawMode、tty.ReadStream#isRaw、tty.ReadStream#isTTY、tty.WriteStream#clearLine、tty.WriteStream#clearScreenDown、tty.WriteStream#cursorTo、tty.WriteStream#getColorDepth、tty.WriteStream#getWindowSize |
| `url` | 25/32 | fileURLToPathBuffer、urlToHttpOptions、URL、URL#toString、URL#toJSON、URL#createObjectURL、URL#revokeObjectURL、URL#canParse |
| `util` | 50/61 | debug、diff、getCallSites、getSystemErrorName、getSystemErrorMap、getSystemErrorMessage、setTraceSigInt、parseEnv |
| `util/types` | 48/61 | callbackify、debuglog、debug、deprecate、diff、format、formatWithOptions、getCallSites |
| `v8` | 52/56 | cachedDataVersionTag、getHeapCodeStatistics、getHeapSpaceStatistics、getCppHeapStatistics、queryObjects、setFlagsFromString、stopCoverage、takeCoverage |
| `vm` | 16/25 | measureMemory、vm.Script#createCachedData、vm.Script#runInContext、vm.Script#runInNewContext、vm.Script#runInThisContext、vm.Script#Type、vm.Module、vm.Module#evaluate |
| `wasi` | 5/5 | WASI、WASI#getImportObject、WASI#start、WASI#initialize、WASI#Type |
| `worker_threads` | 40/43 | getEnvironmentData、isMarkedAsUntransferable、moveMessagePortToContext、postMessageToThread、receiveMessageOnPort、setEnvironmentData、BroadcastChannel、BroadcastChannel#close |
| `zlib` | 36/52 | crc32、createBrotliCompress、createBrotliDecompress、createDeflate、createDeflateRaw、createGunzip、createGzip、createInflate |

## 3. 全局对象缺口

- `localstorage`（global）
- `sessionstorage`（global）
- `MessageEvent`（global）
- `CloseEvent`（global）
- `scheduler`（global）
- `ByteLengthQueuingStrategy`（class）
- `CompressionStream`（class）
- `CountQueuingStrategy`（class）
- `Crypto`（class）
- `CryptoKey`（class）
- `DecompressionStream`（class）
- `EventSource`（class）
- `MessageEvent`（class）
- `MessagePort`（class）
- `Navigator`（class）
- `PerformanceEntry`（class）
- `PerformanceMark`（class）
- `PerformanceMeasure`（class）
- `PerformanceObserver`（class）
- `PerformanceObserverEntryList`（class）
- `PerformanceResourceTiming`（class）
- `ReadableByteStreamController`（class）
- `ReadableStreamBYOBReader`（class）
- `ReadableStreamBYOBRequest`（class）
- `ReadableStreamDefaultController`（class）
- `ReadableStreamDefaultReader`（class）
- `Storage`（class）
- `SubtleCrypto`（class）
- `TextDecoderStream`（class）
- `TextEncoderStream`（class）
- `TransformStreamDefaultController`（class）
- `WebAssembly`（class）
- `WritableStreamDefaultController`（class）
- `WritableStreamDefaultWriter`（class）
- `Process`（class）

## 4. 事件语义缺口（EventEmitter 合同差异样例）

（无）
