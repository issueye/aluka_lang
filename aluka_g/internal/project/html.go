package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/emit"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

func bundleEntry(rt ScriptRuntime, resolver *module.Resolver, entry string, opts Options) (Bundle, error) {
	resolver.SetWebConditions()
	applyResolverAliases(resolver, opts.Aliases)
	switch strings.ToLower(filepath.Ext(entry)) {
	case ".html":
		return bundleHTML(rt, resolver, entry, opts)
	case ".css":
		return bundleCSSFile(entry, opts)
	default:
		return bundleJS(rt, resolver, entry, opts)
	}
}

func bundleHTML(rt ScriptRuntime, resolver *module.Resolver, entry string, opts Options) (Bundle, error) {
	empty := Bundle{Assets: map[string][]byte{}}
	htmlData, err := os.ReadFile(entry)
	if err != nil {
		return empty, fmt.Errorf("read HTML entry %q: %w", entry, err)
	}
	htmlStr := string(htmlData)
	parsed := emit.ParseHTMLEntry(htmlStr)
	replacements := make(map[string]string)
	entryDir := filepath.Dir(entry)
	assets := make(map[string][]byte)
	watch := []string{mustAbs(entry)}
	var extraCSS, preload []string

	for _, script := range parsed.Scripts {
		scriptPath := filepath.Join(entryDir, filepath.FromSlash(script.Original))
		sub, err := bundleEntry(rt, resolver, scriptPath, opts)
		if err != nil {
			return empty, err
		}
		watch = append(watch, sub.Watch...)
		for _, p := range sub.Preload {
			preload = append(preload, withPublicBase(opts.PublicBase, p))
		}
		barrel := PrimaryName(scriptPath)
		for k, v := range sub.Assets {
			if k == barrel {
				continue
			}
			assets[k] = v
			if strings.HasSuffix(strings.ToLower(k), ".css") {
				extraCSS = append(extraCSS, k)
			}
		}
		scriptOut := sub.EntryJS
		if scriptOut == "" {
			scriptOut = barrel
		}
		replacements[script.Original] = withPublicBase(opts.PublicBase, scriptOut)
		if scriptOut != "" {
			preload = append(preload, withPublicBase(opts.PublicBase, scriptOut))
		}
	}

	for _, sheet := range parsed.Stylesheets {
		sheetPath := filepath.Join(entryDir, filepath.FromSlash(sheet.Original))
		cssData, err := os.ReadFile(sheetPath)
		if err != nil {
			return empty, fmt.Errorf("read stylesheet %q: %w", sheetPath, err)
		}
		watch = append(watch, mustAbs(sheetPath))
		content := string(cssData)
		if opts.Minify {
			content = emit.MinifyCSS(content)
		}
		outName := stylesheetOutName(sheet.Original, content, opts)
		if prev, ok := assets[outName]; ok && string(prev) != content {
			return empty, fmt.Errorf("stylesheet output collision %q (from %q)", outName, sheet.Original)
		}
		assets[outName] = []byte(content)
		replacements[sheet.Original] = withPublicBase(opts.PublicBase, outName)
	}

	for i, href := range extraCSS {
		extraCSS[i] = withPublicBase(opts.PublicBase, href)
	}
	rewritten := emit.RewriteHTML(htmlStr, replacements)
	rewritten = injectHTMLStylesheets(rewritten, extraCSS)
	rewritten, err = opts.Host().TransformIndexHTML(rewritten)
	if err != nil {
		return empty, err
	}
	if useNativeESM(opts) {
		rewritten = emit.EnhanceHTML(rewritten, uniqueStrings(preload))
	}
	assets[filepath.Base(entry)] = []byte(rewritten)
	return Bundle{Assets: assets, Watch: uniqueStrings(watch), Preload: uniqueStrings(preload)}, nil
}

func bundleCSSFile(entry string, opts Options) (Bundle, error) {
	empty := Bundle{Assets: map[string][]byte{}}
	cssData, err := os.ReadFile(entry)
	if err != nil {
		return empty, fmt.Errorf("read CSS entry %q: %w", entry, err)
	}
	content := string(cssData)
	if opts.Minify {
		content = emit.MinifyCSS(content)
	}
	base := filepath.Base(entry)
	return Bundle{
		Assets: map[string][]byte{base: []byte(content)},
		Watch:  []string{mustAbs(entry)},
	}, nil
}

func injectHTMLStylesheets(html string, hrefs []string) string {
	var b strings.Builder
	lower := strings.ToLower(html)
	seen := map[string]bool{}
	for _, h := range hrefs {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if strings.Contains(html, h) {
			continue
		}
		b.WriteString(`<link rel="stylesheet" crossorigin href="`)
		b.WriteString(h)
		b.WriteString(`">`)
	}
	extra := b.String()
	if extra == "" {
		return html
	}
	if i := strings.LastIndex(lower, "</head>"); i >= 0 {
		return html[:i] + extra + html[i:]
	}
	return extra + html
}

func stylesheetOutName(original, content string, opts Options) string {
	baseSheet := filepath.Base(original)
	baseName := strings.TrimSuffix(baseSheet, filepath.Ext(baseSheet))
	// 路径参与 hash，避免同 basename 的多 stylesheet 互相覆盖。
	hash := emit.ContentHash(filepath.ToSlash(original), content)
	if useNativeESM(opts) {
		return emit.HashedAssetPathIn(opts.AssetsDir, baseName, hash, ".css")
	}
	return baseName + "-" + hash + ".css"
}
