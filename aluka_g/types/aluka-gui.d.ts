/**
 * Aluka GUI 桌面运行时类型声明。
 *
 * 覆盖两层：
 *   - 主进程：`import { ... } from "aluka:gui"`（亦通过 `Aluka.gui` 访问）
 *   - 前端渲染进程：自动注入的 `window.aluka`
 *
 * 与 internal/gui 及 internal/runtime/globals/aluka_gui.go 保持同源。
 * 平台差异（macOS / Linux 未完整实现的能力）以 `capabilities` 显式降级，
 * 不提供静默成功。
 *
 * ## 窗口事件（win.on / window.aluka.events.on）
 * | 事件名  | 载荷 |
 * |---------|------|
 * | `resize` | `{ width: number, height: number }` |
 * | `move`   | `{ x: number, y: number }` |
 * | `focus`  | `true` |
 * | `blur`   | `false` |
 * | `show` / `hide` / `minimize` / `maximize` / `unmaximize` | 无载荷 |
 * | `close`  | 无载荷（可用 `onCloseRequested` 拦截） |
 * | `fullscreen` | `boolean` |
 */

// ---------------------------------------------------------------------------
// 主进程：aluka:gui
// ---------------------------------------------------------------------------

/** 窗口几何与外观配置。 */
export interface WindowOptions {
  title?: string;
  width?: number;
  height?: number;
  minWidth?: number;
  minHeight?: number;
  maxWidth?: number;
  maxHeight?: number;
  x?: number;
  y?: number;
  center?: boolean;
  /** 是否有原生窗口边框；默认 true。 */
  frame?: boolean;
  /** 背景透明。 */
  transparent?: boolean;
  /** 现代特效："mica" / "acrylic" / "vibrancy" / "none"。 */
  backgroundEffect?: string;
  alwaysOnTop?: boolean;
  /** 是否允许调整大小；默认 true。 */
  resizable?: boolean;
  maximized?: boolean;
  minimized?: boolean;
  /** 初始不透明度 0.0 ~ 1.0。 */
  opacity?: number;
  /** 初始加载 URL（支持 http/https 与 aluka://app/*）。 */
  url?: string;
  /** 直接加载的 HTML 内容。 */
  html?: string;
  /** 前端预加载注入脚本源码。 */
  preloadScript?: string;
  /** 兼容字段名。 */
  preload?: string;
  devTools?: boolean;
  /** 创建时是否初始隐藏。 */
  hidden?: boolean;
}

/** 窗口句柄（主进程操作面向渲染进程的窗口对象）。 */
export interface WindowHandle {
  readonly id: number;

  show(): void;
  hide(): void;
  /** close()：默认走可取消拦截；close(true)：强制关闭。 */
  close(force?: boolean): void;
  center(): void;
  setTitle(title: string): void;
  setSize(width: number, height: number): void;
  getSize(): [number, number];
  setPosition(x: number, y: number): void;
  getPosition(): [number, number];
  setMinSize(width: number, height: number): void;
  setMaxSize(width: number, height: number): void;
  minimize(): void;
  maximize(): void;
  unmaximize(): void;
  toggleMaximize(): void;
  setAlwaysOnTop(on: boolean): void;
  setResizable(on: boolean): void;
  getTitle(): string;
  isMaximized(): boolean;
  isFullscreen(): boolean;
  setFullscreen(on: boolean): void;
  setOpacity(opacity: number): void;
  /** 任务栏进度（0.0 ~ 1.0；<0 清除）。 */
  setProgressBar(progress: number): void;
  /** 任务栏叠加图标（badge），icon 为 .ico 路径；空字符串清除。 */
  setOverlayIcon(iconPath: string): void;
  navigate(url: string): void;
  /** 直接设置 HTML 内容。 */
  setHTML(html: string): void;
  openDevTools(): void;
  executeScript(js: string): void;
  /** 在页面上下文中执行 JavaScript 并异步返回求值结果（支持 Promise）。 */
  evaluate<T = unknown>(js: string): Promise<T>;
  /** 捕获当前页面渲染内容为 PNG（返回 base64 数据及元信息）。 */
  capturePreview(): Promise<{ data: string; mimeType: string; bytes: number }>;
  /** 窗口菜单栏（平台支持时生效）。 */
  setMenu(items: MenuItem[]): void;
  /** 订阅窗口事件，返回取消订阅函数（disposer）。 */
  on(event: string, handler: (data?: unknown) => void): () => void;
  /** 注销窗口事件（传 handler 时仅注销该具体处理函数，不传时注销该事件下所有监听）。 */
  off(event: string, handler?: (data?: unknown) => void): void;
  emit(event: string, data?: unknown): void;
  /** 关闭前拦截：回调返回 true 或 close(true) 才真正关闭。 */
  onCloseRequested(cb: () => boolean): void;
}

/** 屏幕矩形区域。 */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** 显示器信息。 */
export interface DisplayInfo {
  id: string;
  bounds: Rect;
  workArea: Rect;
  scaleFactor: number;
  isPrimary: boolean;
}

/** 运行时平台 GUI 特性检测。 */
export interface Capabilities {
  platform: "windows" | "darwin" | "linux" | "unsupported" | string;
  webview: boolean;
  dialog: boolean;
  evaluate: boolean;
  capturePreview: boolean;
  tray: boolean;
  globalShortcut: boolean;
  menu: boolean;
  clipboard: boolean;
  screen: boolean;
}

/** 原生菜单项。 */
export interface MenuItem {
  id?: string;
  label?: string;
  /** "normal" / "separator" / "checkbox" / "radio"。 */
  type?: string;
  checked?: boolean;
  disabled?: boolean;
  shortcut?: string;
  submenu?: MenuItem[];
  /** 菜单项点击回调（主进程执行）。 */
  click?: (payload: { label?: string; id?: string }) => void;
}

/** 系统对话框配置。 */
export interface DialogOptions {
  title?: string;
  message?: string;
  /** "info" / "warning" / "error" / "question" / "openFile" / "saveFile"。 */
  type?: string;
  buttons?: string[];
  defaultId?: number;
  cancelId?: number;
  filters?: FileFilter[];
  defaultPath?: string;
  directory?: boolean;
  multiple?: boolean;
  /** Electron 风格：openDirectory / multiSelections / openFile。 */
  properties?: string[];
}

export interface FileFilter {
  name?: string;
  extensions: string[];
}

/** 托盘配置。 */
export interface TrayOptions {
  icon?: string;
  tooltip?: string;
  menu?: MenuItem[];
}

/** 托盘句柄。 */
export interface TrayHandle {
  readonly id: number;
  setIcon(icon: string): void;
  setTooltip(tooltip: string): void;
  setMenu(items: MenuItem[]): void;
  destroy(): void;
  on(event: string, handler: (data?: unknown) => void): void;
}

export const capabilities: Capabilities;

export const app: {
  /** 返回取消订阅函数（disposer）。 */
  on(event: "ready" | "before-quit" | "quit", handler: (data?: unknown) => void): () => void;
  off(event: string, handler?: (data?: unknown) => void): void;
  run(): void;
  quit(): void;
  registerRPC(name: string, handler: (params: unknown) => unknown | Promise<unknown>): void;
  unregisterRPC(name: string): void;
  getWindows(): WindowHandle[];
  getWindowById(id: number): WindowHandle | undefined;
};

export function createWindow(options?: WindowOptions): WindowHandle;
export function createTray(options?: TrayOptions): TrayHandle;

export const dialog: {
  showMessageBox(opts?: DialogOptions): Promise<number>;
  showOpenDialog(opts?: DialogOptions): Promise<string[]>;
  showSaveDialog(opts?: DialogOptions): Promise<string | null>;
};

export const clipboard: {
  readText(): Promise<string>;
  writeText(text: string): Promise<{ ok: boolean; error?: string }>;
};

export const screen: {
  getPrimaryDisplay(): Promise<DisplayInfo>;
  getAllDisplays(): Promise<DisplayInfo[]>;
};

export const shell: {
  openPath(path: string): Promise<{ ok: boolean; error?: string }>;
  showItemInFolder(path: string): Promise<{ ok: boolean; error?: string }>;
  openExternal(url: string): Promise<{ ok: boolean; error?: string }>;
};

export const globalShortcut: {
  register(accelerator: string, callback: () => void): unknown;
  unregister(accelerator: string): void;
  unregisterAll(): void;
};

export function setAssetDir(dir: string): void;

// ---------------------------------------------------------------------------
// 前端渲染进程：window.aluka（自动注入）
// ---------------------------------------------------------------------------

declare global {
  interface Window {
    aluka: {
      windowID: number;
      window: {
        minimize(): void;
        maximize(): void;
        unmaximize(): void;
        toggleMaximize(): void;
        close(): void;
        hide(): void;
        show(): void;
        center(): void;
        setTitle(title: string): void;
        setSize(width: number, height: number): void;
        setPosition(x: number, y: number): void;
        setMinSize(width: number, height: number): void;
        setMaxSize(width: number, height: number): void;
        setAlwaysOnTop(on: boolean): void;
        setFullscreen(on: boolean): void;
        openDevTools(): void;
        getSize(): Promise<[number, number]>;
        getPosition(): Promise<[number, number]>;
        isMaximized(): Promise<boolean>;
        isFullscreen(): Promise<boolean>;
        getTitle(): Promise<string>;
      };
      dialog: {
        showOpenDialog(opts?: DialogOptions): Promise<string[]>;
        showSaveDialog(opts?: DialogOptions): Promise<string | null>;
        showMessageBox(opts?: DialogOptions): Promise<number>;
      };
      events: {
        on(event: string, handler: (data?: unknown) => void): void;
        off(event: string, handler: (data?: unknown) => void): void;
        emit(event: string, data?: unknown): void;
      };
      rpc: {
        call(method: string, params?: unknown): Promise<unknown>;
      };
    };
  }
}
