# Aluka GUI API 缺口分析与补齐路线图

> **文档状态**：分析完成 + Phase A 已落地（2026-08-21）  
> **分析范围**：主进程 `Aluka.gui` / `aluka:gui`、前端注入 `window.aluka`、Go 核心 `internal/gui` 三者对齐情况  
> **相关文档**：[aluka-gui-architecture-plan.md](./aluka-gui-architecture-plan.md)、[aluka-ipc-protocol-spec.md](./aluka-ipc-protocol-spec.md)  
> **结论摘要**：**需要优化，也需要有选择地补充**——主干能力（窗口 / 桥接 / RPC / 托盘 / 快捷键 / shell / `--gui` 打包）已够做真实桌面应用；短板集中在「Go 已有但 JS 未暴露」「前后端 API 不对称」「对话框/菜单语义过浅」「类型声明与事件取消订阅缺失」。下文按优先级给出补齐路线，避免一次性做成 Electron 全集。

---

## 更新记录

### 2026-08-21 — Phase A 落地

本次提交完成 **Phase A（API 正确性加固）** 核心目标：

- ✅ **关闭拦截贯通**：`TryClose` / `OnCloseRequested` / `RequestClose` 统一路径；JS `win.close()`、前端 close、原生 WM_CLOSE 均走可取消流程
- ✅ **对话框选项接线**：`NormalizeDialogOptions` 归一化 Electron 风格 `properties`；Win32 `GetOpenFileNameW`/`GetSaveFileNameW` 支持 `filters`/`defaultPath`/`directory`/`multiple`；新增 `showSaveDialog`
- ✅ **创建选项解析**：`parseWindowOptions` 补齐 `maximized`/`minimized`/`opacity`/`preloadScript`（兼容 `preload` 字段名）
- ✅ **WIP 清理**：`Menu` 类型定义、`wsThickFrame` 常量作用域修正、`SetOpacity`/`SetProgressBar`/`SetOverlayIcon` 类型断言接线；工作树可编译
- ✅ **回归验证**：`demo/studio` + `demo/gui-demo` + `aluka_gui_test` + `gui` 包单测全通

---

## 1. 为什么要单独审 GUI API

架构计划文档描述的是**愿景形状**（`new Window`、`Menu.buildFromTemplate`、`preload`、关闭可 `preventDefault` 等），而运行时真实形状是：

| 层 | 入口 | 职责 |
| :--- | :--- | :--- |
| 主进程 JS | `Aluka.gui` 与 `import … from "aluka:gui"` | 宿主生命周期、窗口工厂、托盘、全局快捷键、RPC 注册、资产目录 |
| 前端 WebView | 自动注入的 `window.aluka` | 窗口动作、对话框、事件总线、RPC 调用 |
| Go 核心 | `internal/gui` | 平台 WebView、消息循环、协议、原生对话框/托盘 |

三层一旦漂移，会出现「文档能写、Demo 能跑、业务却踩坑」的体验。本次审查以**可调用的 JS 表面**为第一公民，Go 仅作能力上限对照。

---

## 2. 现状能力盘点（已落地）

### 2.1 主进程：`aluka:gui` / `Aluka.gui`

| API | 状态 | 说明 |
| :--- | :---: | :--- |
| `app.on` / `app.run` / `app.quit` | ✅ | 生命周期；`quit` 会停 JS 事件循环，避免热键残留 |
| `app.registerRPC` | ✅ | 前端 `window.aluka.rpc.call` 可调用 |
| `createWindow(opts)` | ✅ | 工厂函数（非 `new Window`） |
| 窗口：`show/hide/close(force?)/center/setTitle/setSize/getSize/setPosition/getPosition/setMinSize/setMaxSize/minimize/maximize/unmaximize/setAlwaysOnTop/setFullscreen/navigate/openDevTools/executeScript/on/emit` | ✅ | 同步控制为主；`getSize/getPosition/getTitle/isMaximized/isFullscreen` 同步返回 |
| 窗口：`onCloseRequested(fn)` / `setResizable` / `setOpacity` | ✅ | 关闭可取消（回调返回 `true` 或 `close(true)` 真正关闭）；resizable/opacity 运行时控制 |
| `dialog.showMessageBox` / `showOpenDialog` / `showSaveDialog` | ✅ | Promise；`type/buttons/filters/defaultPath/directory/multiple/properties` 已接线（Win32） |
| `shell.openPath` / `showItemInFolder` | ✅ | 返回 `{ ok, error? }`（不 reject） |
| `createTray` + `setIcon/setTooltip/setMenu/destroy/on` | ✅ | 托盘菜单支持 `click` / submenu |
| `globalShortcut.register/unregister/unregisterAll` | ✅ | Windows 实装；其它平台明确报错 |
| `setAssetDir` | ✅ | 产物内嵌模式自动 no-op |

**模块形状**：`aluka:gui` 导出的是整个 `Aluka.gui` 对象（命名导出靠解构），**没有**独立的 `Window` / `Tray` / `Menu` 构造器类。

### 2.2 前端：`window.aluka`

| 命名空间 | 能力 | 状态 |
| :--- | :--- | :---: |
| `window.*` | minimize / maximize / unmaximize / toggleMaximize / close / hide / show / center / setTitle / setSize / setPosition / setMinSize / setMaxSize / setAlwaysOnTop / setFullscreen / openDevTools | ✅ |
| `window.*` 查询 | getSize / getPosition / isMaximized / isFullscreen / getTitle（Promise） | ✅（经 `window_query`） |
| `dialog.*` | showOpenDialog / showSaveDialog / showMessageBox | ✅（主进程 `Aluka.gui.dialog` 三件套已对称） |
| `events.on/off/emit` | 与主进程 `win.on` / `win.emit` 对接 | ✅ |
| `rpc.call` | 调主进程 `registerRPC` | ✅ |
| 无边框 | 拖拽区 + 边缘缩放热区 | ✅（`frame: false`） |

### 2.3 构建与平台

- ✅ Windows WebView2 + `aluka build --gui` 单文件内嵌  
- ✅ macOS WKWebView 第一刀（`aluka://` 顶层 inline 限制仍在）  
- ❌ Linux WebKitGTK（明确错误，非静默 stub）

参考实现：`demo/studio/`（全功能）、`demo/gui-demo/`（最小窗口）。

---

## 3. 主要问题（按严重度）

### 3.1 P0 — 正确性 / 语义断裂（应先修）

| 问题 | 现象 | 影响 | 状态（2026-08-21） |
| :--- | :--- | :--- | :--- |
| **关闭拦截未贯通** | Go 侧有 `OnCloseRequested` / `RequestClose` 设计意图；前端 `close` 可走拦截，但主进程 JS `win.close()` 直接 `Close()`；原生 `WM_CLOSE` 路径也未统一走 `RequestClose` | 无法可靠实现「关窗口 → 藏托盘」；注释与行为不一致 | ✅ 已贯通：`TryClose`/`RequestClose`/`OnCloseRequested`；JS `close(force?)`、前端 close、`WM_CLOSE` 统一走可取消路径 |
| **对话框选项几乎未接线** | `DialogOptions` 含 `type/buttons/filters/defaultPath/directory/multiple` 等；JS 解析与 Win32 实现大多只用 `title/message`，消息框固定图标风格，open 不认多选/目录/过滤器（过滤器结构虽写了但主进程 JS 未传入） | 前端文档示例（`properties: ["openDirectory"]`）与真实行为不符 | ✅ 已接线：`NormalizeDialogOptions` + Win32 `filters`/`defaultPath`/`directory`/`multiple`/buttons 图标映射；macOS 深度选项仍待补 |
| **主进程缺 `showSaveDialog`** | 前端桥已有 `showSaveDialog`，`Aluka.gui.dialog` 没有对称 API | 同能力双入口不一致，主进程无法主动弹保存框 | ✅ 已补齐（返回 `Promise<string|null>`） |
| **创建选项「声明了未解析」** | `WindowOptions` 有 `maximized` / `minimized` / `opacity` / `preloadScript`；`parseWindowOptions` 未读入后三项（及最大化/最小化）；preload 仅 darwin 路径部分使用 | 开发者按类型/注释传参会静默无效 | ✅ 已解析并接线：maximized/minimized 创建即生效；opacity 走 `SetOpacity`；preloadScript 注入 WebView2（darwin 保持原路径） |
| **工作区 WIP 未完成** | 本地未提交改动引入 `SetMenu(*Menu)` 但 `Menu` 类型不存在，且 `wsThickFrame` 作用域错误 → **当前工作树 `internal/gui` 无法通过 `go build`** | 合并前必须修完或回退；新 API 不要半落地 | ✅ 已解决：`Menu` 类型定义（`types.go`）、`wsThickFrame` 提升为包级常量、`SetOpacity`/`SetProgressBar`/`SetOverlayIcon` 类型断言接线；`go build ./...` 通过 |

### 3.2 P1 — 能力已在 Go / 前端一侧，JS 表面缺失或不对称

| Go / 前端已有 | 主进程 JS 缺口 | 建议 | 状态（2026-08-21） |
| :--- | :--- | :--- | :--- |
| `IsMaximized` / `IsFullscreen` / `GetTitle` | 无对应查询 | 对齐前端：同步返回或统一 Promise | ✅ 已补：`isMaximized` / `isFullscreen` / `getTitle` 同步返回 |
| `SetResizable`（NativeWindow） | 无 `setResizable`；创建时 `resizable` 可读 | 补运行时 API | ✅ 已补：`setResizable` + 创建选项 `resizable:false` 生效（去 THICKFRAME） |
| `SetHTML` | 无 `setHTML` | 与 `navigate` 成对暴露 | ⏳ 未做（Go 侧已有 `SetHTML`） |
| `toggleMaximize`（仅前端） | 主进程无 | 补一层薄封装即可 | ⏳ 未做（可先用 `isMaximized` + `maximize/unmaximize` 组合） |
| `UnregisterRPCMethod` | 无 `unregisterRPC` | 热重载 / 插件场景需要 | ⚠️ 半完成：Go 侧 `UnregisterRPCMethod` 已加，JS `app.unregisterRPC` 未暴露 |
| `app.Windows` / `GetWindowByID` | 未暴露 | 多窗口管理刚需 | ⏳ 未做 |
| `events.off`（前端有） | 主进程 `on` 无取消 / 无返回 disposer | 易泄漏；Go `On` 已开始返回取消函数（WIP），JS 应对齐 | ⚠️ 半完成：Go 侧 `On` 返回 disposer + `Off` 已加，JS 侧 `win.off` 未暴露 |
| `shell` 仅路径类 | 无 `openExternal(url)` | 打开浏览器/mailto 是桌面标配 | ⏳ 未做 |

### 3.3 P2 — 产品完整度（对照 Electron / Tauri 的「常用子集」）

下列**不是**必须做成 Electron 1:1，但对「可发布应用」价值高：

1. **应用菜单栏**（窗口 `setMenu` / 全局 `Menu`）— 托盘菜单已有；`Menu` 类型已定义且 `SetMenu(*Menu)` 已接线，但 **Windows 平台实现未落地**（type-assert 静默忽略），仍需 Phase C 实装。
2. **通知**（`Notification` / toast）— 完全缺失。  
3. **剪贴板**（可先复用/封装 Node `clipboard` 语义，或 GUI 命名空间薄封装）。  
4. **屏幕信息**（`screen.getPrimaryDisplay` 等）— 多显示器定位窗口时需要。  
5. **打印 / 页面缩放 / 用户代理** — 可后置。  
6. **权限与安全模型**（导航白名单、`executeScript` 边界、远程 URL 默认拒绝）— 架构上应尽早约定，避免事后破坏兼容。

### 3.4 P3 — 人体工程学与文档债

| 项 | 现状 | 建议 |
| :--- | :--- | :--- |
| API 风格 | 设计稿 `new Window` / `Menu.buildFromTemplate`；实现为 `createWindow` / 菜单数组 | ✅ 工厂风格已冻结为正式 API（2026-08-21 起）；架构计划 §3.3 示例已改写为真实 API，Demo 已是工厂风格 |
| 类型声明 | 无 `aluka:gui` / `window.aluka` 的 `.d.ts` | 增加官方声明文件，IDE 与文档同源 |
| Promise 约定 | dialog reject；shell resolve `{ok}` | 在文档中写死；新增 API 优先「成功 resolve、失败 reject」或统一 Result 对象，二选一 |
| `executeScript` | 无返回值 | 中期改为 Promise\<any\>，对齐前端评估脚本需求 |
| 事件载荷 | 无稳定 schema | 为 `close` / `resize` / `move` / `focus` 等定义可选字段表 |
| macOS `aluka://` | 非完整 scheme handler | 在 API 层暴露 `capabilities` 或平台差异表，避免业务假设 Windows 行为 |

---

## 4. 三层对齐矩阵（摘要）

图例：✅ 齐备 · ⚠️ 部分 · ❌ 缺失 · — 不适用  
（2026-08-21 Phase A 后刷新；主进程查询 API 已与前端对齐为同步返回）

| 能力 | Go | 主进程 JS | 前端 `window.aluka` |
| :--- | :---: | :---: | :---: |
| 创建窗口 + 几何控制 | ✅ | ✅ | ✅（动作）/ ⚠️（查询仅前端） |
| 无边框拖拽/缩放 | ✅ | — | ✅ |
| Mica/Acrylic | ✅ Win | ⚠️ 仅创建选项 | — |
| 关闭可取消 | ✅ | ✅（`onCloseRequested` / `close(force?)`） | ✅ |
| Message / Open / Save 对话框 | ✅（Win32 深选项） | ✅ 三件套 | ✅ 三件套 |
| RPC | ✅ | ✅ register（unregister 未暴露） | ✅ call |
| 事件总线 | ✅（On 返回 disposer） | ✅ on/emit（off 未暴露） | ✅ on/off/emit |
| 托盘 + 菜单 click | ✅ Win | ✅ | — |
| 全局快捷键 | ✅ Win | ✅ | — |
| shell 打开路径 | ✅ | ✅ | ❌（可经 RPC 转发） |
| 窗口菜单栏 | ❌（SetMenu 尚无平台实现） | ❌ | ❌ |
| preload 脚本 | ⚠️ | ✅ 创建选项已解析（Win32 注入） | — |
| 系统通知 | ❌ | ❌ | ❌ |
| Linux WebView | ❌ | ❌ | ❌ |

---

## 5. 推荐原则（避免过度扩张）

1. **先填洞，再造岛**：优先打通已存在的 Go 能力与对称缺口（Save 对话框、查询 API、关闭拦截、选项解析），再新增通知/屏幕等模块。  
2. **双入口必须对称**：凡前端 `window.aluka.X` 有的能力，主进程 `Aluka.gui.X` 应同名同语义（或明确标注「仅渲染进程」并写进 `.d.ts`）。  
3. **工厂 API 为正式表面**：继续以 `createWindow` / `createTray` 为准；不并行维护 `class Window`，除非做纯 TS 薄包装且零运行时双轨。  
4. **平台能力显式降级**：不支持时抛错或返回 `capabilities`，禁止静默成功。  
5. **安全默认值**：`aluka://` 与本地资产优先；远程 URL + `executeScript` 需可审计。  
6. **半成品不进 main**：新 Go 方法必须同时具备类型、平台实现（或 stub 错误）、JS 绑定、单测/ Demo 之一。

---

## 6. 分优先级补齐路线图

### Phase A — API 正确性加固（建议 1 个迭代）

**目标**：行为与注释/文档一致，工作树可编译。  
**状态（2026-08-21）：✅ 已完成并提交。**

- [x] 修复/完成或回退 WIP：`Menu` 类型、`wsThickFrame`、未接线的 `SetOpacity` / `SetProgressBar` / `SetOverlayIcon`
- [x] 统一关闭路径：`win.close()`、前端 close、系统关闭均 `RequestClose` → 可选取消；主进程暴露 `win.onCloseRequested(fn)`（回调返回 `true` 或 `close(true)` 完成真正关闭）
- [x] `parseWindowOptions` 补齐：`maximized` / `minimized` / `opacity` / `preloadScript`（兼容设计稿字段名 `preload`；preload 语义为「源码字符串」）
- [x] 主进程补 `dialog.showSaveDialog`；三端对话框共用 `NormalizeDialogOptions` 解析器
- [x] 加深 Win32 对话框：`filters` / `defaultPath` / `directory` / `multiple`（多选经 NUL 分隔缓冲解析）；macOS 深度选项待补
- [x] 回归：`demo/studio` + `demo/gui-demo` + `aluka_gui_test` + gui 包 `go test` 全通

**验收**：关闭拦截 Demo 可写；Save/Open 过滤器可用；`CGO_ENABLED=0 go test` 覆盖 gui / globals。

### Phase B — 表面补齐与对称（建议 1 个迭代）

**目标**：主进程与前端窗口控制面齐平，多窗口可管。

- [ ] 主进程：`getTitle` / `isMaximized` / `isFullscreen` / `toggleMaximize` / `setResizable` / `setHTML`  
- [ ] `app.getWindows()` / `app.getWindowById(id)`  
- [ ] `app.unregisterRPC(name)`；`win.off` 或 `on` 返回 disposer  
- [ ] `shell.openExternal(url)`  
- [ ] 官方 `types/aluka-gui.d.ts`（主进程 + 可选 `WindowAluka` 全局）  
- [ ] 重写 [aluka-gui-architecture-plan.md](./aluka-gui-architecture-plan.md) §3.3 示例为真实 API

**验收**：仅用主进程 API 可复刻 studio 前端窗口按钮逻辑；TS 工程可类型检查。

### Phase C — 桌面生态子集（按产品需要排期）

**目标**：可发布工具类应用的「菜单 + 通知 + 屏幕」。

- [ ] `Menu` / `setApplicationMenu` / `win.setMenu`（复用现有 `MenuItem` 模型）  
- [ ] `Notification.show`（Windows Toast / macOS NSUserNotification 路径需纯 Go）  
- [ ] `screen` 最小集：主屏 bounds / workArea / scaleFactor  
- [ ] 窗口事件增强：`resize` / `move` / `focus` / `blur`（带几何载荷）  
- [ ] `executeScript` → `Promise` 回传结果

**验收**：无第三方 native 模块即可做出带菜单栏 + 托盘 + 通知的工具应用。

### Phase D — 平台与安全（中长期）

- [ ] macOS 完整 `aluka://` scheme（或文档化永久限制 + 构建期断言）  
- [ ] Linux WebKitGTK  
- [ ] 导航/权限策略、CSP 辅助、远程内容默认拒绝  
- [ ] 与 AIP（跨进程 IPC）边界澄清：GUI 桥保持 JSON WebMessage；重负载走 `aluka:ipc`

---

## 7. 建议的目标 API 草图（Phase A–B 冻结范围）

仅列**建议冻结**的表面；**✅ = 已实现（2026-08-21）**，未标记项仍属规划。

```ts
// aluka:gui（主进程）
export const app: {
  on(event: "ready" | "before-quit" | "quit", cb: (data?: unknown) => void): () => void;   // ⚠️ on 已实现，disposer 未回传 JS
  run(): void;                                                                             // ✅
  quit(): void;                                                                            // ✅
  registerRPC(name: string, handler: (params: unknown) => unknown | Promise<unknown>): void; // ✅
  unregisterRPC(name: string): void;                                                        // ⏳ Go 已备，JS 未暴露
  getWindows(): WindowHandle[];                                                             // ⏳
  getWindowById(id: number): WindowHandle | undefined;                                      // ⏳
};

export function createWindow(opts?: WindowOptions): WindowHandle;   // ✅
export function createTray(opts?: TrayOptions): TrayHandle;        // ✅
export const dialog: {
  showMessageBox(opts: MessageBoxOptions): Promise<number>;        // ✅
  showOpenDialog(opts: OpenDialogOptions): Promise<string[]>;      // ✅
  showSaveDialog(opts: SaveDialogOptions): Promise<string | null>; // ✅
};
export const shell: {
  openPath(path: string): Promise<{ ok: boolean; error?: string }>;           // ✅
  showItemInFolder(path: string): Promise<{ ok: boolean; error?: string }>;   // ✅
  openExternal(url: string): Promise<{ ok: boolean; error?: string }>;        // ⏳
};
export const globalShortcut: { /* 现状保留 */ };   // ✅
export function setAssetDir(dir: string): void;     // ✅

interface WindowHandle {
  readonly id: number;                              // ✅
  // 控制 + 查询 + on/emit + onCloseRequested / close 可取消
  show(): void; hide(): void; close(force?: boolean): void;  // ✅（close 默认走拦截）
  center(): void; setTitle(t: string): void; setSize(w: number, h: number): void;
  getSize(): [number, number]; setPosition(x: number, y: number): void; getPosition(): [number, number];
  setMinSize(w: number, h: number): void; setMaxSize(w: number, h: number): void;
  minimize(): void; maximize(): void; unmaximize(): void;
  setAlwaysOnTop(on: boolean): void; setFullscreen(on: boolean): void; openDevTools(): void;
  setResizable(on: boolean): void;                  // ✅
  getTitle(): string; isMaximized(): boolean; isFullscreen(): boolean;  // ✅
  setOpacity(o: number): void;                      // ✅
  navigate(url: string): void; executeScript(js: string): void; on(event: string, cb: (data?: unknown) => void): void; emit(event: string, data?: unknown): void;
  onCloseRequested(cb: () => boolean): void;        // ✅（返回 false / 不返回 → 取消；返回 true 或 close(true) → 关闭）
}
```

前端继续以 `window.aluka` 为唯一注入名；新增能力优先与上表同名。

---

## 8. 明确不建议近期做的事

- 复制 Electron 全量 `BrowserWindow` / `webContents` 表面  
- 在 GUI JSON 桥上塞二进制大包（应走 AIP）  
- 同时维护 `new Window` 与 `createWindow` 两套运行时  
- 在 Linux 未就绪前用静默 stub「假装成功」  
- 无 `.d.ts` 与 Demo 更新的「纯文档先行」大改命名

---

## 9. 结论

| 维度 | 判断 |
| :--- | :--- |
| 是否需要优化 | **是** — 关闭语义、对话框深度、选项解析、前后端对称、文档与实现统一（**Phase A 已落地，P0 问题全部关闭**） |
| 是否需要补充 | **是，但分阶段** — Phase B 补齐已有能力暴露（`.d.ts`、`unregisterRPC`/`off` 暴露、多窗口管理、`openExternal`）；Phase C 按产品加菜单/通知/屏幕 |
| 当前能否做应用 | **能**（Windows 优先）— `demo/studio` 已覆盖 RPC/托盘/快捷键/无边框/关闭拦截；缺口主要在「专业桌面体验」与「API 可信度」 |
| 最大风险 | 设计文档超前 + WIP 半成品入树 → 开发者按愿景编码失败；以及关闭/对话框静默错误行为（**Phase A 已收敛，当前工作树可编译**） |

**建议的下一动作**：Phase A 已完成并提交；下一迭代落 Phase B（`.d.ts` 类型声明、`app.unregisterRPC` / `win.off` 对称暴露、`app.getWindows` / `getWindowById`、`shell.openExternal`、`setHTML` / `toggleMaximize`），并回写架构计划文档 §3.3 示例为真实 API（已随本次提交更新）。
