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

	// Dev 为 true 时等价于 Vite serve：command=serve、mode=development。
	Dev bool
	// Command / Mode 非空时覆盖 Dev 推导（"build"|"serve"、"production"|"development"）。
	Command string
	Mode    string

	CLIOutdir      bool
	CLIMinify      bool
	CLIVueCompiler bool

	Plugins plugin.Host
}

// BuildEnv 返回配置/插件钩子使用的 command 与 mode。
func (o Options) BuildEnv() (command, mode string) {
	if o.Dev {
		command, mode = "serve", "development"
	} else {
		command, mode = "build", "production"
	}
	if o.Command != "" {
		command = o.Command
	}
	if o.Mode != "" {
		mode = o.Mode
	}
	return command, mode
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
