// SFC 编译后端抽象（包文档见 sfc.go）。
//
// 两个后端共享同一产物契约：输出 ESM facade 与可选的生成模块，import 的
// 'vue' 运行时 helper 由 graph 按 node_modules 正常解析（Vite 式架构：编译器
// 不内嵌运行时）。后端选择是构建期开关，禁止静默回退——两后端产物语义不同
//（subset: 选项式 render(_ctx)；official: script setup 展开 + hoisted 模板）。

package vue

// GeneratedModule 是 SFC 编译器产生的虚拟模块。Name 是相对 facade 的稳定
// 文件名；扩展名决定 JS/TS 前端选择。
type GeneratedModule struct {
	Name   string
	Source string
}

// CompileResult 是单个 SFC 的编译结果。Facade 保留 .vue 模块身份；生成的
// script/template 模块拥有独立词法作用域，避免 helper 与用户绑定冲突。
type CompileResult struct {
	Facade  string
	Modules []GeneratedModule
}

// Compiler 是 .vue 单文件组件的编译后端。
// Compile 将 SFC 源码编译为 facade 与可选生成模块；name 用于模块名和诊断。
type Compiler interface {
	Compile(src, name string) (*CompileResult, error)
}

// SubsetCompiler 是默认后端：纯 Go 子集实现（TransformSFC）。
// 快（微秒级）、离线、零引擎依赖；模板子集外的语法构建期明确报错。
type SubsetCompiler struct{}

// Compile 编译 SFC（见 TransformSFC 的子集约定）。默认 subset 仍输出单模块，
// 保持既有产物行为。
func (SubsetCompiler) Compile(src, name string) (*CompileResult, error) {
	code, err := TransformSFC(src, name)
	if err != nil {
		return nil, err
	}
	return &CompileResult{Facade: code}, nil
}
