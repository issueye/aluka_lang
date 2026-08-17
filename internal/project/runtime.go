// Package project 是 web 构建工作台：从 cmd 迁入的打包逻辑，经 ScriptRuntime
// 与具体 JS 引擎解耦，并调度 Vite 风格插件钩子。
package project

import "github.com/aluka-lang/aluka/internal/engine"

// ScriptRuntime 是配置发现与插件钩子需要的最小脚本能力。
// 不为 Value 再造一套引擎镜像；插件对象就是 engine.Value。
type ScriptRuntime interface {
	Require(id, parent string) (engine.Value, error)
}
