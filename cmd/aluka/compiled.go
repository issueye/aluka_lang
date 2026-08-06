// 产物模式（B2：payload 自附加）——`aluka build --compile` 产物的运行端。
//
// 产物文件 = aluka 基座 + payload + footer。启动时在 main() 最早期检测
// 自身尾部 footer（零开销：只读最后 FooterSize 字节），命中则进入产物
// 模式：解析 payload → 取出预编译的入口模块 → 复用引擎引导与
// Loader.RunPrecompiled 执行（跳过 parse/转换/编译全链路）。
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// detectCompiledPayload 检测当前可执行文件是否携带编译产物 payload。
// 无 payload（普通 aluka）时零开销返回 (nil, false)，不影响正常启动。
// 校验和失败视为非产物（回退正常模式，M4 将升级为告警）。
func detectCompiledPayload() ([]byte, bool) {
	exe, err := os.Executable()
	if err != nil {
		return nil, false
	}
	f, err := os.Open(exe)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < compile.FooterSize {
		return nil, false
	}
	// 只读尾部 FooterSize 字节。
	if _, err := f.Seek(info.Size()-compile.FooterSize, io.SeekStart); err != nil {
		return nil, false
	}
	footer := make([]byte, compile.FooterSize)
	if _, err := io.ReadFull(f, footer); err != nil {
		return nil, false
	}
	offset, length, sum, ok := compile.ParseFooter(footer)
	if !ok || offset > uint64(info.Size()) {
		return nil, false
	}
	// 读取 payload 并校验 sha256。
	if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, false
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(f, payload); err != nil {
		return nil, false
	}
	if !compile.VerifyPayload(payload, sum) {
		return nil, false
	}
	return payload, true
}

// runCompiled 执行编译产物（payload 已校验）。返回进程退出码。
func runCompiled(payload []byte) int {
	manifest, data, err := compile.ParsePayload(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aluka: "+err.Error())
		return 1
	}
	entry := manifest.Entry
	mod, err := manifest.LoadModule(data, entry)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aluka: "+err.Error())
		return 1
	}

	// 引擎引导（与 runModule 一致）：VM 引擎 + 全局对象 + 内置模块。
	eng := interpreter.NewVMEngine()
	defer eng.Shutdown()
	ctx, err := eng.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aluka: "+err.Error())
		return 1
	}
	defer ctx.Close()
	if err := registerRuntimeGlobals(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "aluka: "+err.Error())
		return 1
	}

	loader := modmodule.NewLoader(ctx)
	builtin.RegisterAll(loader)
	_ = builtin.InstallGetBuiltinModule(ctx, loader)
	// M2：嵌入式模块存储——require/import 按构建期解析映射加载嵌入模块。
	loader.SetEmbedded(compile.NewEmbedded(manifest, data))

	isESM := manifest.ModuleTypeOf(entry) == compile.ModuleTypeESM
	if _, err := loader.RunPrecompiled(entry, mod, isESM); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	// 进入事件循环：处理定时器/异步任务，直到无 pending 任务。
	if vm, ok := ctx.(interface{ RunLoop() }); ok {
		vm.RunLoop()
	}
	return 0
}
