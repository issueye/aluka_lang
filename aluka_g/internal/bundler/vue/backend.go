// SFC 编译后端抽象（包文档见 sfc.go）。
//
// 两个后端共享同一产物契约：输出 ESM facade 与可选的生成模块，import 的
// 'vue' 运行时 helper 由 graph 按 node_modules 正常解析（Vite 式架构：编译器
// 不内嵌运行时）。后端选择是构建期开关，禁止静默回退——两后端产物语义不同
//（subset: 选项式 render(_ctx)；official: script setup 展开 + hoisted 模板）。
//
// 样式对齐 @vitejs/plugin-vue：每个 <style> 是独立虚拟 CSS 模块，由 facade
// 副作用 import 进入 graph，再交给 emit.BundleCSS（相当于 Vite 的 CSS 插件）。

package vue

// GeneratedModule 是 SFC 编译器产生的虚拟模块。Name 是相对 facade 的稳定
// 文件名；扩展名决定 JS/TS 前端选择。
type GeneratedModule struct {
	Name   string
	Source string
}

// CompileRequest 是一次 SFC 编译的输入。Filename 用于解析相对 src 与诊断；
// 测试可只填 Source+Name。
type CompileRequest struct {
	Source   string
	Name     string // 虚拟路径 / 诊断名（如 "src/Counter.vue"）
	Filename string // SFC 绝对路径；src 相对此目录解析
	ReadFile func(path string) ([]byte, error)
	Resolve  func(spec, from string) (string, error)
}

// CompileResult 是单个 SFC 的编译结果。Facade 保留 .vue 模块身份；生成的
// script/template 模块拥有独立词法作用域，避免 helper 与用户绑定冲突。
type CompileResult struct {
	Facade     string
	Modules    []GeneratedModule
	Styles     []GeneratedModule // 按块索引的虚拟 CSS（Foo.vue.__aluka_style.0.css）
	ExtraFiles []string          // 外部 src 绝对路径（对应 Rollup addWatchFile）
}

// Compiler 是 .vue 单文件组件的编译后端。
type Compiler interface {
	Compile(req CompileRequest) (*CompileResult, error)
}

// SubsetCompiler 是默认后端：纯 Go 子集实现（TransformSFC）。
// 快（微秒级）、离线、零引擎依赖；模板子集外的语法构建期明确报错。
type SubsetCompiler struct{}

// Compile 编译 SFC（见 TransformSFC 的子集约定）。默认 subset 仍把
// script+template 内联进 facade；每个 <style> 输出独立虚拟 CSS 模块。
func (SubsetCompiler) Compile(req CompileRequest) (*CompileResult, error) {
	return transformSFC(req)
}

// compileNamed 是测试便捷封装：无文件系统的 CompileRequest。
func compileNamed(c Compiler, src, name string) (*CompileResult, error) {
	return c.Compile(CompileRequest{Source: src, Name: name})
}
