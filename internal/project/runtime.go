// Package project 是 web 构建工作台：项目配置、插件会话、HTML 入口与写盘。
// JS 模块图的 shake/emit 在 bundler/webemit；本包经 ScriptRuntime 加载配置脚本。
package project

import "github.com/aluka-lang/aluka/internal/engine"

// ScriptRuntime 是配置发现与插件钩子需要的最小脚本能力。
// 不为 Value 再造一套引擎镜像；插件对象就是 engine.Value。
type ScriptRuntime interface {
	Require(id, parent string) (engine.Value, error)
}
