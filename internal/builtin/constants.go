package builtin

import (
	"runtime"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewConstants implements the legacy constants/node:constants builtin.
// 常量数据见 constants_data.go（由 node 22.23.1 Windows 实测生成）。
func NewConstants(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	for _, p := range nodeConstPairs {
		setConstValue(m, p.name, p.val)
	}
	return m, nil
}

// fsConstantsObject 构造 fs.constants 对象（键集合与数值按平台对齐 Node）。
// Windows/Linux 键集合有差异：O_SYNC/O_DIRECTORY/O_NOFOLLOW/O_DSYNC/O_RSYNC/
// S_IFBLK/S_IFSOCK 仅在 POSIX 平台暴露。
func fsConstantsObject() engine.Value {
	m := engine.NewObject()
	vals := map[string]int{}
	for _, p := range nodeConstPairs {
		if iv, ok := p.val.(int); ok {
			vals[p.name] = iv
		}
	}
	names := fsConstNames
	for k, v := range posixFsExtraConstants() {
		vals[k] = v
		names = append(names, k)
	}
	for _, n := range names {
		if v, ok := vals[n]; ok {
			_ = m.Set(n, engine.IntValue(v))
		}
	}
	return m
}

// osConstantsObject 构造 os.constants 对象（signals/priority/errno/dlopen/UV）。
func osConstantsObject() engine.Value {
	m := engine.NewObject()

	sig := engine.NewObject()
	for _, p := range osSignalPairs {
		_ = sig.Set(p.name, engine.IntValue(p.val))
	}
	_ = m.Set("signals", sig)

	pri := engine.NewObject()
	for _, p := range osPriorityPairs {
		_ = pri.Set(p.name, engine.IntValue(p.val))
	}
	_ = m.Set("priority", pri)

	errno := engine.NewObject()
	for _, p := range osErrnoPairs {
		if strings.HasPrefix(p.name, "WSA") && runtime.GOOS != "windows" {
			continue // WSA* 错误码仅 Windows 平台暴露
		}
		_ = errno.Set(p.name, engine.IntValue(p.val))
	}
	_ = m.Set("errno", errno)

	dlopen := engine.NewObject()
	_ = m.Set("dlopen", dlopen)
	_ = m.Set("UV_UDP_REUSEADDR", engine.IntValue(4))
	return m
}

// setConstValue 把常量写入对象（支持 int/string/float 值）。
func setConstValue(obj engine.Object, name string, val interface{}) {
	switch v := val.(type) {
	case int:
		_ = obj.Set(name, engine.IntValue(v))
	case int64:
		_ = obj.Set(name, engine.IntValue(int(v)))
	case float64:
		_ = obj.Set(name, engine.Number(v))
	case string:
		_ = obj.Set(name, engine.Str(v))
	}
}
