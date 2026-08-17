// Package plugin 实现 Vite 风格 web 插件调度：Go 只按钩子名有序调用，
// 不写死具体插件。JS 插件对象由配置脚本保活，经 ScriptRuntime 取值。
package plugin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// Host 是构建管线调用的钩子集合。无插件时用 Nop。
type Host interface {
	SetEnv(command, mode string)
	ConfigJSON(in string) (string, error)
	ConfigResolved(info string) error
	BuildStart() error
	ResolveId(id, importer string) (resolved string, ok bool, err error)
	Load(id string) (code string, ok bool, err error)
	Transform(id, code string) (string, error)
	TransformIndexHTML(html string) (string, error)
	GenerateBundle(files []string) (map[string]string, error)
	WriteBundle(files []string) error
	CloseBundle() error
}

// Nop 空实现。
type Nop struct{}

func (Nop) SetEnv(string, string)                              {}
func (Nop) ConfigJSON(in string) (string, error)               { return in, nil }
func (Nop) ConfigResolved(string) error                        { return nil }
func (Nop) BuildStart() error                                  { return nil }
func (Nop) ResolveId(string, string) (string, bool, error)     { return "", false, nil }
func (Nop) Load(string) (string, bool, error)                  { return "", false, nil }
func (Nop) Transform(_, code string) (string, error)           { return code, nil }
func (Nop) TransformIndexHTML(html string) (string, error)     { return html, nil }
func (Nop) GenerateBundle([]string) (map[string]string, error) { return nil, nil }
func (Nop) WriteBundle([]string) error                         { return nil }
func (Nop) CloseBundle() error                                 { return nil }

// JSHost 调度配置里的 plugins 数组。
type JSHost struct {
	plugins []engine.Value
	command string
	mode    string
}

// SetEnv 设置 config 钩子第二参（Vite 风格 command/mode）。
func (h *JSHost) SetEnv(command, mode string) {
	if command == "" {
		command = "build"
	}
	if mode == "" {
		mode = "production"
	}
	h.command = command
	h.mode = mode
}

// NewJSHost 从 JS 数组（或空）构造 Host。
func NewJSHost(list engine.Value) Host {
	if list == nil || list.IsUndefined() || list.IsNull() {
		return Nop{}
	}
	obj, ok := list.AsObject()
	if !ok {
		return Nop{}
	}
	n := arrayLen(obj)
	if n == 0 {
		return Nop{}
	}
	h := &JSHost{plugins: make([]engine.Value, 0, n)}
	for i := 0; i < n; i++ {
		item, err := obj.Get(strconv.Itoa(i))
		if err != nil || item == nil || item.IsUndefined() || item.IsNull() {
			continue
		}
		if b, ok := item.Bool(); ok && !b {
			continue
		}
		h.plugins = append(h.plugins, flattenPlugin(item)...)
	}
	if len(h.plugins) == 0 {
		return Nop{}
	}
	return h
}

func flattenPlugin(v engine.Value) []engine.Value {
	obj, ok := v.AsObject()
	if !ok {
		return nil
	}
	if n := arrayLen(obj); n > 0 && !hasOwn(obj, "name") {
		var out []engine.Value
		for i := 0; i < n; i++ {
			item, err := obj.Get(strconv.Itoa(i))
			if err != nil || item == nil {
				continue
			}
			out = append(out, flattenPlugin(item)...)
		}
		return out
	}
	return []engine.Value{v}
}

func (h *JSHost) ConfigJSON(in string) (string, error) {
	cfg, err := objectFromJSON(emptyObj(in))
	if err != nil {
		return "", err
	}
	command, mode := h.command, h.mode
	if command == "" {
		command = "build"
	}
	if mode == "" {
		mode = "production"
	}
	env := engine.NewObject()
	_ = env.Set("command", engine.Str(command))
	_ = env.Set("mode", engine.Str(mode))
	for _, p := range h.plugins {
		out, err := h.call(p, "config", cfg, env)
		if err != nil {
			return "", err
		}
		if out == nil || out.IsUndefined() || out.IsNull() {
			continue // 允许就地改 cfg
		}
		if obj, ok := out.AsObject(); ok {
			if _, isFn := out.AsFunction(); isFn {
				continue
			}
			for _, k := range obj.Keys() {
				val, getErr := obj.Get(k)
				if getErr != nil || val == nil || val.IsUndefined() {
					continue
				}
				_ = cfg.Set(k, val)
			}
			continue
		}
		if patch := valueJSON(out); patch != "" && patch != "undefined" && patch != "null" {
			merged, mergeErr := mergeJSON(valueJSON(cfg), patch)
			if mergeErr != nil {
				return "", hookErr(p, "config", mergeErr)
			}
			cfg, err = objectFromJSON(merged)
			if err != nil {
				return "", hookErr(p, "config", err)
			}
		}
	}
	flattenBuildFields(cfg)
	return valueJSON(cfg), nil
}

func (h *JSHost) ConfigResolved(info string) error {
	for _, p := range h.plugins {
		if _, err := h.call(p, "configResolved", engine.Str(info)); err != nil {
			return err
		}
	}
	return nil
}

func (h *JSHost) BuildStart() error {
	for _, p := range h.plugins {
		if _, err := h.call(p, "buildStart"); err != nil {
			return err
		}
	}
	return nil
}

func (h *JSHost) ResolveId(id, importer string) (string, bool, error) {
	for _, p := range h.plugins {
		out, err := h.call(p, "resolveId", engine.Str(id), engine.Str(importer))
		if err != nil {
			return "", false, err
		}
		if out != nil && out.Type() == engine.TypeBoolean {
			if b, ok := out.Bool(); ok && !b {
				return "", true, nil // external
			}
			continue
		}
		if s, ok := stringResult(out); ok {
			return s, true, nil
		}
	}
	return "", false, nil
}

func (h *JSHost) Load(id string) (string, bool, error) {
	for _, p := range h.plugins {
		out, err := h.call(p, "load", engine.Str(id))
		if err != nil {
			return "", false, err
		}
		if s, ok := stringResult(out); ok {
			return s, true, nil
		}
	}
	return "", false, nil
}

func (h *JSHost) Transform(id, code string) (string, error) {
	cur := code
	for _, p := range h.plugins {
		out, err := h.call(p, "transform", engine.Str(cur), engine.Str(id))
		if err != nil {
			return "", err
		}
		if s, ok := stringResult(out); ok {
			cur = s
		}
	}
	return cur, nil
}

func (h *JSHost) TransformIndexHTML(html string) (string, error) {
	cur := html
	for _, p := range h.plugins {
		out, err := h.call(p, "transformIndexHtml", engine.Str(cur))
		if err != nil {
			return "", err
		}
		if s, ok := stringResult(out); ok {
			cur = s
		}
	}
	return cur, nil
}

func (h *JSHost) GenerateBundle(files []string) (map[string]string, error) {
	extra := map[string]string{}
	arg := engine.Str(mustJSON(files))
	for _, p := range h.plugins {
		out, err := h.call(p, "generateBundle", arg)
		if err != nil {
			return nil, err
		}
		mergeFileMap(extra, out)
	}
	if len(extra) == 0 {
		return nil, nil
	}
	return extra, nil
}

func (h *JSHost) WriteBundle(files []string) error {
	arg := engine.Str(mustJSON(files))
	for _, p := range h.plugins {
		if _, err := h.call(p, "writeBundle", arg); err != nil {
			return err
		}
	}
	return nil
}

func (h *JSHost) CloseBundle() error {
	for _, p := range h.plugins {
		if _, err := h.call(p, "closeBundle"); err != nil {
			return err
		}
	}
	return nil
}

func (h *JSHost) call(plugin engine.Value, hook string, args ...engine.Value) (engine.Value, error) {
	obj, ok := plugin.AsObject()
	if !ok {
		return engine.Undefined(), nil
	}
	fnVal, err := obj.Get(hook)
	if err != nil || fnVal == nil || fnVal.IsUndefined() || fnVal.IsNull() {
		return engine.Undefined(), nil
	}
	if _, ok := fnVal.AsFunction(); !ok {
		return engine.Undefined(), nil
	}
	// 绑定 this=插件对象，便于方法内读 this.name / 自有 helper（仍无 emitFile/resolve）。
	out, err := interpreter.CallWithThis(fnVal, plugin, args)
	if err != nil {
		return nil, hookErr(plugin, hook, err)
	}
	if out != nil && out.Type() == engine.TypeObject && !out.IsNull() {
		if _, isFn := out.AsFunction(); !isFn {
			if then, _ := objGet(out, "then"); then != nil && then.IsFunction() {
				return nil, hookErr(plugin, hook, fmt.Errorf("async hook is not supported"))
			}
		}
	}
	return out, nil
}

func hookErr(plugin engine.Value, hook string, err error) error {
	name := "plugin"
	if obj, ok := plugin.AsObject(); ok {
		if v, e := obj.Get("name"); e == nil && v != nil && !v.IsUndefined() {
			if s := strings.TrimSpace(v.String()); s != "" {
				name = s
			}
		}
	}
	return fmt.Errorf("plugin %s: %s: %w", name, hook, err)
}

func arrayLen(obj engine.Object) int {
	nVal, err := obj.Get("length")
	if err != nil || nVal == nil {
		return 0
	}
	n, ok := nVal.Int()
	if !ok || n <= 0 {
		return 0
	}
	return n
}

func hasOwn(obj engine.Object, key string) bool {
	v, err := obj.Get(key)
	return err == nil && v != nil && !v.IsUndefined()
}

func objGet(v engine.Value, key string) (engine.Value, error) {
	obj, ok := v.AsObject()
	if !ok {
		return engine.Undefined(), nil
	}
	return obj.Get(key)
}

func stringResult(v engine.Value) (string, bool) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return "", false
	}
	if v.Type() == engine.TypeString {
		return v.String(), true
	}
	if obj, ok := v.AsObject(); ok {
		if code, err := obj.Get("code"); err == nil && code != nil && !code.IsUndefined() && !code.IsNull() {
			return code.String(), true
		}
		if id, err := obj.Get("id"); err == nil && id != nil && !id.IsUndefined() && !id.IsNull() {
			return id.String(), true
		}
	}
	return "", false
}

func valueJSON(v engine.Value) string {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return ""
	}
	if v.Type() == engine.TypeString {
		s := v.String()
		if json.Valid([]byte(s)) {
			return s
		}
		return ""
	}
	if obj, ok := v.AsObject(); ok {
		m := map[string]any{}
		for _, k := range obj.Keys() {
			val, err := obj.Get(k)
			if err != nil || val == nil || val.IsUndefined() {
				continue
			}
			m[k] = jsToAny(val)
		}
		b, err := json.Marshal(m)
		if err != nil {
			return ""
		}
		return string(b)
	}
	return ""
}

func jsToAny(v engine.Value) any {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return nil
	}
	if b, ok := v.Bool(); ok && v.Type() == engine.TypeBoolean {
		return b
	}
	if f, ok := v.Float(); ok && v.Type() == engine.TypeNumber {
		return f
	}
	if v.Type() == engine.TypeString {
		return v.String()
	}
	if obj, ok := v.AsObject(); ok {
		if n := arrayLen(obj); n > 0 && !hasOwn(obj, "name") {
			arr := make([]any, 0, n)
			for i := 0; i < n; i++ {
				item, _ := obj.Get(strconv.Itoa(i))
				arr = append(arr, jsToAny(item))
			}
			return arr
		}
		m := map[string]any{}
		for _, k := range obj.Keys() {
			item, _ := obj.Get(k)
			m[k] = jsToAny(item)
		}
		return m
	}
	return v.String()
}

func mergeJSON(base, patch string) (string, error) {
	var a, b map[string]any
	if err := json.Unmarshal([]byte(emptyObj(base)), &a); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(emptyObj(patch)), &b); err != nil {
		return "", err
	}
	for k, v := range b {
		a[k] = v
	}
	out, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func objectFromJSON(s string) (engine.Object, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(emptyObj(s)), &m); err != nil {
		return nil, err
	}
	return anyToObject(m), nil
}

func anyToObject(m map[string]any) engine.Object {
	o := engine.NewObject()
	for k, v := range m {
		_ = o.Set(k, anyToValue(v))
	}
	return o
}

func anyToValue(v any) engine.Value {
	switch x := v.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(x)
	case float64:
		return engine.Number(x)
	case string:
		return engine.Str(x)
	case map[string]any:
		return anyToObject(x)
	case []any:
		elems := make([]engine.Value, len(x))
		for i, item := range x {
			elems[i] = anyToValue(item)
		}
		return engine.NewArray(elems)
	default:
		return engine.Str(fmt.Sprint(x))
	}
}

// flattenBuildFields 把 Vite 风格 build.outDir / assetsDir / minify 提到顶层。
func flattenBuildFields(cfg engine.Object) {
	buildVal, err := cfg.Get("build")
	if err != nil || buildVal == nil || buildVal.IsUndefined() || buildVal.IsNull() {
		return
	}
	build, ok := buildVal.AsObject()
	if !ok {
		return
	}
	copyIfAbsent := func(fromKey, toKey string) {
		cur, _ := cfg.Get(toKey)
		if cur != nil && !cur.IsUndefined() && !cur.IsNull() {
			return
		}
		v, e := build.Get(fromKey)
		if e != nil || v == nil || v.IsUndefined() || v.IsNull() {
			return
		}
		_ = cfg.Set(toKey, v)
	}
	copyIfAbsent("outDir", "outDir")
	copyIfAbsent("assetsDir", "assetsDir")
	copyIfAbsent("minify", "minify")
}

func emptyObj(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "undefined" || s == "null" {
		return "{}"
	}
	return s
}

func mergeFileMap(dst map[string]string, v engine.Value) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return
	}
	if v.Type() == engine.TypeString {
		var m map[string]string
		if json.Unmarshal([]byte(v.String()), &m) == nil {
			for k, val := range m {
				dst[k] = val
			}
		}
		return
	}
	obj, ok := v.AsObject()
	if !ok {
		return
	}
	for _, k := range obj.Keys() {
		item, err := obj.Get(k)
		if err != nil || item == nil {
			continue
		}
		dst[k] = item.String()
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}
