package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/webconfig"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// ApplyConfig 加载项目配置、跑 plugin.config / configResolved，写回 opts。
func ApplyConfig(rt ScriptRuntime, entry string, opts *Options) error {
	if opts == nil {
		return nil
	}
	cwd, _ := os.Getwd()
	root := webconfig.FindRoot(entry, cwd)
	sess, err := webconfig.LoadSession(rt, root)
	if err != nil {
		return err
	}
	host := sess.Plugins
	if host == nil {
		host = opts.Host()
	}
	if sess.Result != nil {
		raw, err := json.Marshal(sess.Result)
		if err != nil {
			return err
		}
		patched, err := host.ConfigJSON(string(raw))
		if err != nil {
			return err
		}
		if strings.TrimSpace(patched) != "" {
			if err := json.Unmarshal([]byte(patched), sess.Result); err != nil {
				return err
			}
		}
	}
	applied := webconfig.Applied{
		OutDir:      opts.OutDir,
		AssetsDir:   opts.AssetsDir,
		PublicBase:  opts.PublicBase,
		Minify:      opts.Minify,
		VueCompiler: opts.VueCompiler,
		Alias:       opts.Aliases,
		Define:      opts.Defines,
	}
	webconfig.Apply(&applied, sess.Result, webconfig.CLIOverrides{
		OutDir:      opts.CLIOutdir,
		Minify:      opts.CLIMinify,
		VueCompiler: opts.CLIVueCompiler,
	})
	opts.OutDir = applied.OutDir
	opts.AssetsDir = applied.AssetsDir
	opts.PublicBase = applied.PublicBase
	opts.Minify = applied.Minify
	opts.VueCompiler = applied.VueCompiler
	opts.Aliases = absAliasMap(root, applied.Alias)
	opts.Defines = applied.Define
	opts.Plugins = host
	// 配置文件里的 outDir 相对项目根（与 Vite 一致）；CLI --outdir 仍相对 cwd。
	if !opts.CLIOutdir && sess.Result != nil && sess.Result.OutDir != "" && opts.OutDir != "" && !filepath.IsAbs(opts.OutDir) {
		opts.OutDir = filepath.Join(root, filepath.FromSlash(opts.OutDir))
	}
	info, err := json.Marshal(map[string]any{
		"outDir":      opts.OutDir,
		"assetsDir":   opts.AssetsDir,
		"base":        opts.PublicBase,
		"minify":      opts.Minify,
		"vueCompiler": opts.VueCompiler,
	})
	if err != nil {
		return err
	}
	return host.ConfigResolved(string(info))
}

func absAliasMap(root string, aliases map[string]string) map[string]string {
	if len(aliases) == 0 {
		return aliases
	}
	out := make(map[string]string, len(aliases))
	for k, v := range aliases {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if filepath.IsAbs(v) {
			out[k] = v
			continue
		}
		out[k] = filepath.Join(root, filepath.FromSlash(v))
	}
	return out
}

func withPublicBase(base, rel string) string {
	base = strings.TrimSpace(base)
	if base == "" || base == "./" || base == "." {
		return rel
	}
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + rel
}

func applyResolverAliases(resolver *module.Resolver, aliases map[string]string) {
	for from, to := range aliases {
		resolver.AddAlias(from, to)
	}
}
