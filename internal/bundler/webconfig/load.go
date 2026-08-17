// Package webconfig 用 ScriptRuntime 跑一段可替换的发现脚本，把项目打包配置
// 归一成 web 构建选项。Go 侧只负责找脚本、执行、套用归一字段；
// vite.config / vue.config 等文件名与形态映射写在脚本里，避免写死。
package webconfig

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/plugin"
	"github.com/aluka-lang/aluka/internal/engine"
)

//go:embed default-loader.js
var defaultLoaderJS string

// Runtime 是配置加载需要的最小脚本能力（由 project.ScriptRuntime 满足）。
type Runtime interface {
	Require(id, parent string) (engine.Value, error)
}

// Result 是发现脚本返回的归一化选项（未知字段已在脚本侧丢掉）。
type Result struct {
	Source      string            `json:"source"`
	OutDir      string            `json:"outDir"`
	AssetsDir   string            `json:"assetsDir"`
	Base        string            `json:"base"`
	Minify      *bool             `json:"minify"`
	VueCompiler string            `json:"vueCompiler"`
	Alias       map[string]string `json:"alias"`
	Define      map[string]string `json:"define"`
}

// Session 保活配置脚本里的 plugins 对象（不进 JSON）。
type Session struct {
	Result  *Result
	Plugins plugin.Host
}

// CLIOverrides 标记命令行已显式给出的字段（这些字段配置文件不得覆盖）。
type CLIOverrides struct {
	OutDir      bool
	Minify      bool
	VueCompiler bool
}

// Load 在项目根执行配置发现脚本。ALUKA_WEB_CONFIG 指向自定义脚本时用它，
// 否则用包内 default-loader.js。没有可识别的项目配置时返回空 Result。
func Load(rt Runtime, root string) (*Result, error) {
	sess, err := LoadSession(rt, root)
	if err != nil {
		return nil, err
	}
	return sess.Result, nil
}

// LoadSession 与 Load 相同，但保留 plugins 数组供 Host 调度。
// env 可选：[0]=command（默认 build）、[1]=mode（默认 production）。
func LoadSession(rt Runtime, root string, env ...string) (*Session, error) {
	command, mode := "build", "production"
	if len(env) > 0 && strings.TrimSpace(env[0]) != "" {
		command = strings.TrimSpace(env[0])
	}
	if len(env) > 1 && strings.TrimSpace(env[1]) != "" {
		mode = strings.TrimSpace(env[1])
	}
	if rt == nil {
		return nil, fmt.Errorf("webconfig: nil runtime")
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return &Session{Result: &Result{}, Plugins: plugin.Nop{}}, nil
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("webconfig: resolve root: %w", err)
	}
	script, cleanup, err := resolveLoaderScript()
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	parent := filepath.Join(absRoot, "package.json")
	ns, err := rt.Require(script, parent)
	if err != nil {
		return nil, fmt.Errorf("webconfig: load loader script %s: %w", script, err)
	}
	obj, ok := ns.AsObject()
	if !ok {
		return nil, fmt.Errorf("webconfig: loader script did not export an object")
	}

	if fnVal, err := obj.Get("loadWebSession"); err == nil && fnVal != nil && !fnVal.IsUndefined() {
		fn, ok := fnVal.AsFunction()
		if !ok {
			return nil, fmt.Errorf("webconfig: loadWebSession is not a function")
		}
		out, err := fn.Call([]engine.Value{engine.Str(absRoot), engine.Str(command), engine.Str(mode)})
		if err != nil {
			return nil, fmt.Errorf("webconfig: %w", err)
		}
		sess, err := sessionFromValue(out)
		if err != nil {
			return nil, err
		}
		sess.Plugins.SetEnv(command, mode)
		return sess, nil
	}

	fnVal, err := obj.Get("loadWebConfigJSON")
	if err != nil || fnVal == nil || fnVal.IsUndefined() {
		return nil, fmt.Errorf("webconfig: loader script missing loadWebConfigJSON")
	}
	fn, ok := fnVal.AsFunction()
	if !ok {
		return nil, fmt.Errorf("webconfig: loadWebConfigJSON is not a function")
	}
	out, err := fn.Call([]engine.Value{engine.Str(absRoot), engine.Str(command), engine.Str(mode)})
	if err != nil {
		return nil, fmt.Errorf("webconfig: %w", err)
	}
	res, err := resultFromJSON(out.String())
	if err != nil {
		return nil, err
	}
	return &Session{Result: res, Plugins: plugin.Nop{}}, nil
}

func sessionFromValue(v engine.Value) (*Session, error) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return &Session{Result: &Result{}, Plugins: plugin.Nop{}}, nil
	}
	obj, ok := v.AsObject()
	if !ok {
		return nil, fmt.Errorf("webconfig: loadWebSession did not return an object")
	}
	jsonVal, err := obj.Get("json")
	if err != nil {
		return nil, fmt.Errorf("webconfig: loadWebSession missing json: %w", err)
	}
	res, err := resultFromJSON(jsonVal.String())
	if err != nil {
		return nil, err
	}
	pluginsVal, _ := obj.Get("plugins")
	return &Session{Result: res, Plugins: plugin.NewJSHost(pluginsVal)}, nil
}

func resultFromJSON(raw string) (*Result, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "undefined" {
		return &Result{}, nil
	}
	var res Result
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil, fmt.Errorf("webconfig: invalid normalized JSON: %w", err)
	}
	return &res, nil
}

// FindRoot 从入口目录向上找最近的 package.json，但不超过 cwd
// （避免临时项目没有 package.json 时误用上层无关仓库）。
func FindRoot(entry, cwd string) string {
	absCwd := ""
	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			absCwd = abs
		}
	} else if wd, err := os.Getwd(); err == nil {
		absCwd = wd
	}

	starts := make([]string, 0, 3)
	if entry != "" {
		if abs, err := filepath.Abs(entry); err == nil {
			info, statErr := os.Stat(abs)
			if statErr == nil && !info.IsDir() {
				starts = append(starts, filepath.Dir(abs))
			} else {
				starts = append(starts, abs)
			}
		}
	}
	if absCwd != "" {
		starts = append(starts, absCwd)
	}
	for _, start := range starts {
		dir := start
		for {
			if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
				return dir
			}
			if absCwd != "" && sameDir(dir, absCwd) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if len(starts) > 0 {
		return starts[0]
	}
	return cwd
}

func sameDir(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if strings.EqualFold(a, b) {
		return true
	}
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func resolveLoaderScript() (string, func(), error) {
	if override := strings.TrimSpace(os.Getenv("ALUKA_WEB_CONFIG")); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", nil, fmt.Errorf("webconfig: ALUKA_WEB_CONFIG: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", nil, fmt.Errorf("webconfig: ALUKA_WEB_CONFIG %s: %w", abs, err)
		}
		return abs, nil, nil
	}
	f, err := os.CreateTemp("", "aluka-webconfig-*.cjs")
	if err != nil {
		return "", nil, fmt.Errorf("webconfig: temp loader: %w", err)
	}
	if _, err := f.WriteString(defaultLoaderJS); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("webconfig: write loader: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

// Apply 把归一化结果写进 dst（仅填充 dst 中尚未被 CLI 占用的字段）。
func Apply(dst *Applied, src *Result, cli CLIOverrides) {
	if dst == nil || src == nil {
		return
	}
	if !cli.OutDir && src.OutDir != "" && dst.OutDir == "" {
		dst.OutDir = src.OutDir
	}
	if src.AssetsDir != "" && dst.AssetsDir == "" {
		dst.AssetsDir = src.AssetsDir
	}
	if src.Base != "" && dst.PublicBase == "" {
		dst.PublicBase = src.Base
	}
	if !cli.Minify && src.Minify != nil {
		dst.Minify = *src.Minify
	}
	if !cli.VueCompiler && src.VueCompiler != "" && dst.VueCompiler == "" {
		dst.VueCompiler = src.VueCompiler
	}
	if len(src.Alias) > 0 {
		if dst.Alias == nil {
			dst.Alias = map[string]string{}
		}
		for k, v := range src.Alias {
			if _, ok := dst.Alias[k]; !ok {
				dst.Alias[k] = v
			}
		}
	}
	if len(src.Define) > 0 {
		if dst.Define == nil {
			dst.Define = map[string]string{}
		}
		for k, v := range src.Define {
			if _, ok := dst.Define[k]; !ok {
				dst.Define[k] = v
			}
		}
	}
}

// Applied 是套用到 CLI 构建选项上的可变字段。
type Applied struct {
	OutDir      string
	AssetsDir   string
	PublicBase  string
	Minify      bool
	VueCompiler string
	Alias       map[string]string
	Define      map[string]string
}
