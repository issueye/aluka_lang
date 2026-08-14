package astutil

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// oldCollectRefsReflect 是从 git 3a512b7 抄录的旧反射实现（用于对拍差分）。
func oldCollectRefsReflect(node interface{}) map[string]int {
	refs := make(map[string]int)
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		}
	}
	collect := func(node interface{}) {
		switch n := node.(type) {
		case *ast.Identifier:
			refs[n.Name]++
		case *ast.VarDeclarator:
			if n.Init != nil {
				walk(reflect.ValueOf(n.Init))
			}
		case *ast.FunctionDecl:
			for _, d := range n.Defaults {
				if d != nil {
					walk(reflect.ValueOf(d))
				}
			}
			walk(reflect.ValueOf(n.Body))
		case *ast.ClassDecl:
			walk(reflect.ValueOf(n.SuperClass))
			walk(reflect.ValueOf(n.Body))
		default:
			walk(reflect.ValueOf(node))
		}
	}
	var walkStmt func(v reflect.Value)
	walkStmt = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collect(v.Elem().Interface())
			walkStmt(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			collect(v.Interface())
			walkStmt(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walkStmt(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walkStmt(v.Field(i))
			}
		}
	}
	walkStmt(reflect.ValueOf(node))
	return refs
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestCollectRefsDiffVsOldReflection 对比新旧 CollectRefs 在真实 TS 源码上的
// 引用集合差异：找出"旧实现计入、新实现未计入"的引用（这类可能导致 tree-shake
// 错误剪除 import binding）。
func TestCollectRefsDiffVsOldReflection(t *testing.T) {
	candidates := []string{
		"E:/code/github/pi/packages/coding-agent/src/config.ts",
		"E:/codes/github/pi/packages/coding-agent/src/config.ts",
	}
	var src []byte
	var err error
	for _, c := range candidates {
		if src, err = os.ReadFile(c); err == nil {
			break
		}
	}
	if len(src) == 0 {
		t.Skipf("config.ts not readable: %v", err)
	}
	prog, err := parser.ParseModule(string(src))
	if err != nil {
		t.Fatalf("parse config.ts: %v", err)
	}
	old := oldCollectRefsReflect(prog)
	newr := CollectRefs(prog)
	var lost []string
	for name := range old {
		if newr[name] == 0 {
			lost = append(lost, name)
		}
	}
	sort.Strings(lost)
	t.Logf("旧计入、新未计入的引用名（可能导致误剪）: %v", lost)
	var gained []string
	for name := range newr {
		if old[name] == 0 {
			gained = append(gained, name)
		}
	}
	sort.Strings(gained)
	t.Logf("新计入、旧未计入的引用名: %v", gained)
	// 输出关键名：readFileSync 等内置导入。
	for _, probe := range []string{"readFileSync", "dirname", "join", "resolve", "homedir", "fileURLToPath", "accessSync", "existsSync", "realpathSync", "sep", "win32", "basename", "normalizePath", "spawnProcessSync"} {
		t.Logf("probe %-18s old=%d new=%d", probe, old[probe], newr[probe])
	}
	_ = strings.Join
	_ = sortedKeys
}
