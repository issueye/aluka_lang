package module

// data: URL 模块加载（动态 import("data:text/javascript;base64,…")）。
//
// jiti 的 ESM 转译主路径之一：转译后源码以 data: URL 交回宿主 import()。
// Node 语义：data:text/javascript（及 application/javascript）恒按 ESM
// 模块 URL 执行；解码失败或媒体类型不支持报错。源码不自带相对依赖
// （jiti 转译时已把相对 specifier 重写为绝对路径），故执行入口与文件
// 模式无关，仅依赖绝对路径/内置模块的运行时解析。

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// loadDataSource 解码并执行一个 data: URL 模块。
// data: URL 不落盘也不可缓存：不走 bcCache，直接 parse → ESM lower → 编译。
func (l *Loader) loadDataSource(dataURL string) (engine.Value, error) {
	src, err := decodeDataURLSource(dataURL)
	if err != nil {
		return engine.Undefined(), err
	}
	unit, err := ParseSourceUnit(src, dataURL, ModuleESM)
	if err != nil {
		return engine.Undefined(), err
	}
	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		return engine.Undefined(), fmt.Errorf("module: bytecode compilation requires the bytecode VM engine")
	}
	prog := ast.DeepCopy(unit.Program)
	transformed := TransformESMToCJS(prog, unit.Path)
	wrapped := WrapESMAST(transformed, unit.Path)
	mod, err := vm.CompileAST(wrapped, dataURL)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", dataURL, err)
	}
	return l.RunPrecompiled(dataURL, mod, true)
}

// decodeDataURLSource 解析 data: URL 载荷（仅 text/application/javascript
// 媒体类型），返回解码后的模块源码。支持 ;base64 与 percent-encoding 两种
// 编码（RFC 2397）。
func decodeDataURLSource(dataURL string) ([]byte, error) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return nil, fmt.Errorf("module: import: not a data: URL")
	}
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, fmt.Errorf("module: import: invalid data: URL")
	}
	meta, payload := rest[:comma], rest[comma+1:]
	if !strings.HasPrefix(meta, "text/javascript") && !strings.HasPrefix(meta, "application/javascript") {
		return nil, fmt.Errorf("module: import: unsupported data: media type %q (only JavaScript)", meta)
	}
	var raw []byte
	var err error
	if strings.Contains(meta, ";base64") {
		raw, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("module: import: invalid base64 in data: URL: %w", err)
		}
	} else {
		decoded, unescapeErr := url.PathUnescape(payload)
		if unescapeErr != nil {
			return nil, fmt.Errorf("module: import: invalid percent-encoding in data: URL: %w", unescapeErr)
		}
		raw = []byte(decoded)
	}
	return raw, nil
}