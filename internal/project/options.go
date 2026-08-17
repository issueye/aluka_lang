package project

import "github.com/aluka-lang/aluka/internal/bundler/plugin"

// Options 是一次 web 构建的选项。cmd 只做 flag → 本结构的映射，不另维护语义拷贝。
type Options struct {
	Format      string
	GlobalName  string
	OutDir      string
	OutFile     string
	AssetsDir   string
	PublicBase  string
	VueCompiler string
	Minify      bool
	TreeShake   bool
	Sourcemap   bool
	Aliases     map[string]string
	Defines     map[string]string

	CLIOutdir      bool
	CLIMinify      bool
	CLIVueCompiler bool

	Plugins plugin.Host
}

// Host 返回插件调度器；无插件时为 no-op。
func (o Options) Host() plugin.Host {
	if o.Plugins == nil {
		return plugin.Nop{}
	}
	return o.Plugins
}

// Bundle 是单入口 web 构建产物。
type Bundle struct {
	Assets  map[string][]byte
	Watch   []string
	EntryJS string
	Preload []string
}
