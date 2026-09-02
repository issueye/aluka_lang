// node:test 快照断言：快照文件读写、--update-snapshots 与 JSON 序列化（保持键顺序）。

package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// snapshotState 记录当前测试文件的快照状态（文件路径 + 调用计数）。
var snapshotMu sync.Mutex

var snapshotFile string // 当前测试文件对应的快照文件路径

var snapshotCount int // 当前文件内 snapshot 调用计数

var updateSnapshots bool // --test-update-snapshots 模式

// SetSnapshotFile 由测试运行器设置当前测试文件（用于快照定位）。
func SetSnapshotFile(testFilePath string) {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	snapshotFile = testFilePath + ".snapshot"
	snapshotCount = 0
	currentTestFilePath = testFilePath
}

// SetUpdateSnapshots 启用/禁用快照更新模式（--test-update-snapshots）。
func SetUpdateSnapshots(update bool) {
	snapshotMu.Lock()
	updateSnapshots = update
	snapshotMu.Unlock()
}

// snapshotAssert 实现快照断言。
// 序列化格式（Node 22）：字符串 → JSON 字符串（带引号）；对象 → JSON 2 空格。
// 快照文件：<testfile>.snapshot，条目格式 exports[`snap <n>`] = `\n<serialized>\n`;
func snapshotAssert(vm *interpreter.VM, value engine.Value) (engine.Value, error) {
	snapshotMu.Lock()
	file := snapshotFile
	snapshotCount++
	idx := snapshotCount
	update := updateSnapshots
	snapshotMu.Unlock()

	if file == "" {
		return engine.Undefined(), fmt.Errorf("%w: snapshot: no test file context", engine.ErrAssertion)
	}

	// 序列化。
	var serialized string
	switch value.Type() {
	case engine.TypeString:
		b, _ := json.Marshal(value.String())
		serialized = string(b)
	default:
		serialized = snapshotJSON(vm, value)
	}
	entry := fmt.Sprintf("exports[`snap %d`] = `\n%s\n`;\n", idx, serialized)

	// 读取现有快照文件。
	existing := ""
	if data, err := os.ReadFile(file); err == nil {
		existing = string(data)
	}

	if update {
		// 更新模式：写回整个文件（保留其他条目，替换当前编号）。
		merged := snapshotReplaceEntry(existing, idx, entry)
		_ = os.MkdirAll(filepath.Dir(file), 0755)
		return engine.Undefined(), os.WriteFile(file, []byte(merged), 0644)
	}

	// 比较模式。
	if existing == "" {
		return engine.Undefined(), fmt.Errorf("%w: snapshot not found (run with --test-update-snapshots)", engine.ErrAssertion)
	}
	if !strings.Contains(existing, fmt.Sprintf("exports[`snap %d`]", idx)) {
		return engine.Undefined(), fmt.Errorf("%w: snapshot %d not found in %s", engine.ErrAssertion, idx, file)
	}
	if strings.Contains(existing, entry) {
		return engine.Undefined(), nil // 匹配
	}
	return engine.Undefined(), fmt.Errorf("%w: snapshot %d mismatch", engine.ErrAssertion, idx)
}

// snapshotReplaceEntry 替换/追加编号条目。
func snapshotReplaceEntry(existing string, idx int, entry string) string {
	marker := fmt.Sprintf("exports[`snap %d`]", idx)
	// 按块分割（每个条目以 exports[`snap N`] 开头）。
	lines := strings.Split(existing, "\n")
	var out []string
	replaced := false
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			block := strings.Join(cur, "\n")
			if strings.Contains(block, marker) {
				out = append(out, entry)
				replaced = true
			} else {
				out = append(out, block)
			}
		}
		cur = nil
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "exports[`snap ") {
			flush()
		}
		cur = append(cur, ln)
	}
	flush()
	if !replaced {
		out = append(out, entry)
	}
	return strings.Join(out, "\n")
}

// snapshotJSON 序列化快照值：对象 → JSON 2 空格缩进（Node 快照格式）；
// 键序保持插入序；不做 HTML 转义。
func snapshotJSON(vm *interpreter.VM, value engine.Value) string {
	data, err := snapToGo(value)
	if err != nil {
		return value.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return value.String()
	}
	s := buf.String()
	return strings.TrimRight(s, "\n")
}

// snapOrdered 保持插入键序的 JSON 容器。
type snapOrdered struct {
	keys []string
	vals []interface{}
}

func (o *snapOrdered) MarshalJSON() ([]byte, error) {
	parts := make([]string, len(o.keys))
	for i, k := range o.keys {
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.vals[i])
		if err != nil {
			return nil, err
		}
		parts[i] = string(kb) + ":" + string(vb)
	}
	return []byte("{" + strings.Join(parts, ",") + "}"), nil
}

// snapToGo 把 engine.Value 转为可 JSON 序列化的 Go 结构（插入键序）。
func snapToGo(v engine.Value) (interface{}, error) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return nil, nil
	}
	switch v.Type() {
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return b, nil
	case engine.TypeNumber:
		f, _ := v.Float()
		return f, nil
	case engine.TypeString:
		return v.String(), nil
	}
	if arr, ok := v.(*engine.ArrayValue); ok {
		out := make([]interface{}, 0, len(arr.Elems()))
		for _, e := range arr.Elems() {
			ev, err := snapToGo(e)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		return out, nil
	}
	if o, ok := v.AsObject(); ok {
		so := &snapOrdered{}
		for _, k := range o.Keys() {
			if k == "length" {
				continue
			}
			val, _ := o.Get(k)
			if val.IsFunction() || val.IsUndefined() {
				continue
			}
			ev, err := snapToGo(val)
			if err != nil {
				return nil, err
			}
			so.keys = append(so.keys, k)
			so.vals = append(so.vals, ev)
		}
		return so, nil
	}
	return nil, nil
}
