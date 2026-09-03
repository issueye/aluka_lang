package vue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sfcExternals 是已加载的外部 src 块，供 subset 内联与 official 注入。
type sfcExternals struct {
	Script      *externalBlock
	ScriptSetup *externalBlock
	Template    *externalBlock
	Styles      []externalBlock
	Files       []string
}

type externalBlock struct {
	Content  string
	Filename string
}

func (req CompileRequest) displayName() string {
	if req.Name != "" {
		return req.Name
	}
	if req.Filename != "" {
		return filepath.ToSlash(req.Filename)
	}
	return "SFC.vue"
}

func (req CompileRequest) readFile(path string) ([]byte, error) {
	if req.ReadFile != nil {
		return req.ReadFile(path)
	}
	return os.ReadFile(path)
}

func (req CompileRequest) resolveSrc(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("%s: empty src", req.displayName())
	}
	from := req.Filename
	if from == "" {
		return "", fmt.Errorf("%s: external src %q requires a real SFC filename", req.displayName(), spec)
	}
	if req.Resolve != nil {
		resolved, err := req.Resolve(spec, from)
		if err != nil {
			return "", fmt.Errorf("%s: cannot resolve src %q: %w", req.displayName(), spec, err)
		}
		return filepath.Clean(resolved), nil
	}
	if filepath.IsAbs(spec) {
		return filepath.Clean(spec), nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(from), filepath.FromSlash(spec))), nil
}

func loadBlockSrc(req CompileRequest, block *sfcBlock, extra *[]string) (*externalBlock, error) {
	if block == nil || block.attr("src") == "" {
		return nil, nil
	}
	abs, err := req.resolveSrc(block.attr("src"))
	if err != nil {
		return nil, err
	}
	data, err := req.readFile(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read src %q: %w", req.displayName(), abs, err)
	}
	*extra = append(*extra, abs)
	return &externalBlock{Content: string(data), Filename: abs}, nil
}

func loadExternals(req CompileRequest, blocks []sfcBlock) (*sfcExternals, error) {
	out := &sfcExternals{}
	for i := range blocks {
		b := &blocks[i]
		if b.attr("src") == "" {
			continue
		}
		ext, err := loadBlockSrc(req, b, &out.Files)
		if err != nil {
			return nil, err
		}
		if ext == nil {
			continue
		}
		switch {
		case b.Tag == "script" && b.isSetup():
			out.ScriptSetup = ext
		case b.Tag == "script":
			out.Script = ext
		case b.Tag == "template":
			out.Template = ext
		case b.Tag == "style":
			out.Styles = append(out.Styles, *ext)
		}
	}
	return out, nil
}

func blockContent(block *sfcBlock, ext *externalBlock) string {
	if ext != nil {
		return ext.Content
	}
	if block == nil {
		return ""
	}
	return block.Content
}

func rejectUnsupportedStyle(name string, st sfcBlock) error {
	if st.has("module") {
		return fmt.Errorf("%s: <style module> is not supported yet", name)
	}
	lang := strings.ToLower(strings.TrimSpace(st.attr("lang")))
	if lang != "" && lang != "css" {
		return fmt.Errorf("%s: <style lang=%q> is not supported yet; only css", name, st.attr("lang"))
	}
	return nil
}
