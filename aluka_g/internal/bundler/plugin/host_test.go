package plugin

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

func TestNopHostPassthrough(t *testing.T) {
	h := Nop{}
	got, err := h.Transform("a.ts", "export const n=1")
	if err != nil || got != "export const n=1" {
		t.Fatalf("Transform = %q, %v", got, err)
	}
	html, err := h.TransformIndexHTML("<html></html>")
	if err != nil || html != "<html></html>" {
		t.Fatalf("HTML = %q, %v", html, err)
	}
}

func TestJSHostTransformAndResolve(t *testing.T) {
	tf := engine.NewFunction("transform", func(args []engine.Value) (engine.Value, error) {
		code := args[0].String() + "/*p*/"
		return engine.Str(code), nil
	})
	resolve := engine.NewFunction("resolveId", func(args []engine.Value) (engine.Value, error) {
		if args[0].String() == "virtual:ok" {
			return engine.Str("\x00virtual:ok"), nil
		}
		return engine.Undefined(), nil
	})
	load := engine.NewFunction("load", func(args []engine.Value) (engine.Value, error) {
		if args[0].String() == "\x00virtual:ok" {
			return engine.Str("export const v=1"), nil
		}
		return engine.Undefined(), nil
	})
	p := engine.NewObject()
	_ = p.Set("name", engine.Str("demo"))
	_ = p.Set("transform", tf)
	_ = p.Set("resolveId", resolve)
	_ = p.Set("load", load)
	host := NewJSHost(engine.NewArray([]engine.Value{p}))

	out, err := host.Transform("a.ts", "export const n=1")
	if err != nil || out != "export const n=1/*p*/" {
		t.Fatalf("transform = %q, %v", out, err)
	}
	id, ok, err := host.ResolveId("virtual:ok", "main.ts")
	if err != nil || !ok || id != "\x00virtual:ok" {
		t.Fatalf("resolveId = %q ok=%v err=%v", id, ok, err)
	}
	code, ok, err := host.Load(id)
	if err != nil || !ok || code != "export const v=1" {
		t.Fatalf("load = %q ok=%v err=%v", code, ok, err)
	}
}

func TestJSHostGenerateBundle(t *testing.T) {
	fn := engine.NewFunction("generateBundle", func(args []engine.Value) (engine.Value, error) {
		o := engine.NewObject()
		_ = o.Set("plugin-manifest.json", engine.Str(args[0].String()))
		return o, nil
	})
	p := engine.NewObject()
	_ = p.Set("name", engine.Str("mani"))
	_ = p.Set("generateBundle", fn)
	host := NewJSHost(engine.NewArray([]engine.Value{p}))
	extra, err := host.GenerateBundle([]string{"a.js"})
	if err != nil {
		t.Fatal(err)
	}
	if extra["plugin-manifest.json"] != `["a.js"]` {
		t.Fatalf("extra = %#v", extra)
	}
}

func TestJSHostResolveIdFalseExternal(t *testing.T) {
	resolve := engine.NewFunction("resolveId", func(args []engine.Value) (engine.Value, error) {
		if args[0].String() == "ext:skip" {
			return engine.Boolean(false), nil
		}
		return engine.Undefined(), nil
	})
	p := engine.NewObject()
	_ = p.Set("name", engine.Str("ext"))
	_ = p.Set("resolveId", resolve)
	host := NewJSHost(engine.NewArray([]engine.Value{p}))
	id, ok, err := host.ResolveId("ext:skip", "main.ts")
	if err != nil || !ok || id != "" {
		t.Fatalf("external resolveId = %q ok=%v err=%v", id, ok, err)
	}
}

func TestJSHostConfigNestedBuildFillsTop(t *testing.T) {
	nested := engine.NewFunction("config", func(args []engine.Value) (engine.Value, error) {
		build := engine.NewObject()
		_ = build.Set("outDir", engine.Str("nested-out"))
		_ = build.Set("minify", engine.Boolean(true))
		o := engine.NewObject()
		_ = o.Set("build", build)
		return o, nil
	})
	p := engine.NewObject()
	_ = p.Set("name", engine.Str("nested"))
	_ = p.Set("config", nested)
	host := NewJSHost(engine.NewArray([]engine.Value{p}))
	out, err := host.ConfigJSON(`{"source":"t"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nested-out") {
		t.Fatalf("expected flattened outDir, got %s", out)
	}
	if !strings.Contains(out, `"minify":true`) && !strings.Contains(out, `"minify": true`) {
		t.Fatalf("expected flattened minify, got %s", out)
	}
}

func TestJSHostConfigInPlaceMutate(t *testing.T) {
	mutate := engine.NewFunction("config", func(args []engine.Value) (engine.Value, error) {
		obj, _ := args[0].AsObject()
		_ = obj.Set("outDir", engine.Str("from-plugin"))
		return engine.Undefined(), nil
	})
	p := engine.NewObject()
	_ = p.Set("name", engine.Str("mutate"))
	_ = p.Set("config", mutate)
	host := NewJSHost(engine.NewArray([]engine.Value{p}))
	out, err := host.ConfigJSON(`{"source":"t","outDir":"orig"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "from-plugin") {
		t.Fatalf("in-place mutate failed: %s", out)
	}
}

func TestJSHostConfigEnvFromSetEnv(t *testing.T) {
	seen := engine.NewObject()
	cfg := engine.NewFunction("config", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 1 {
			env, _ := args[1].AsObject()
			cmd, _ := env.Get("command")
			mode, _ := env.Get("mode")
			_ = seen.Set("command", cmd)
			_ = seen.Set("mode", mode)
		}
		return engine.Undefined(), nil
	})
	p := engine.NewObject()
	_ = p.Set("name", engine.Str("env"))
	_ = p.Set("config", cfg)
	host := NewJSHost(engine.NewArray([]engine.Value{p})).(*JSHost)
	host.SetEnv("serve", "development")
	if _, err := host.ConfigJSON(`{}`); err != nil {
		t.Fatal(err)
	}
	cmd, _ := seen.Get("command")
	mode, _ := seen.Get("mode")
	if cmd.String() != "serve" || mode.String() != "development" {
		t.Fatalf("env = %s/%s", cmd.String(), mode.String())
	}
}
