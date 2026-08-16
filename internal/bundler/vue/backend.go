// SFC 编译后端抽象（包文档见 sfc.go）。
//
// 两个后端共享同一产物契约：输出 ESM 模块源码，import 的 'vue' 运行时
// helper 由 graph 按 node_modules 正常解析（Vite 式架构：编译器不内嵌
// 运行时）。后端选择是构建期开关，禁止静默回退——两后端产物语义不同
//（subset: 选项式 render(_ctx)；official: script setup 展开 + hoisted 模板）。

package vue

// Compiler 是 .vue 单文件组件的编译后端。
// Transform 将 SFC 源码编译为等价 ESM 模块源码；name 仅用于错误信息。
type Compiler interface {
	Transform(src, name string) (string, error)
}

// SubsetCompiler 是默认后端：纯 Go 子集实现（TransformSFC）。
// 快（微秒级）、离线、零引擎依赖；模板子集外的语法构建期明确报错。
type SubsetCompiler struct{}

// Transform 编译 SFC（见 TransformSFC 的子集约定）。
func (SubsetCompiler) Transform(src, name string) (string, error) {
	return TransformSFC(src, name)
}
