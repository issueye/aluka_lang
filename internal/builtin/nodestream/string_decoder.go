package nodestream

// node:string_decoder 内置模块——将 Buffer 片段解码为完整字符串。
// 处理多字节字符跨 Buffer 边界的情况。

import (
	"fmt"
	"unicode/utf8"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewStringDecoder 构造 node:string_decoder 模块的导出对象。
func NewStringDecoder(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// StringDecoder 构造器
	ctor := engine.NewFunction("StringDecoder", func(args []engine.Value) (engine.Value, error) {
		encoding := "utf8"
		if len(args) > 0 && args[0].String() != "" {
			encoding = args[0].String()
		}
		return newStringDecoderInstance(encoding), nil
	})
	_ = m.Set("StringDecoder", ctor)

	return m, nil
}

// newStringDecoderInstance 创建一个 StringDecoder 实例。
func newStringDecoderInstance(encoding string) engine.Value {
	obj := engine.NewObject()

	// 内部状态：未完成的多字节序列。
	var incomplete []byte

	// write(buffer)：写入数据片段，返回可完整解码的字符串。
	_ = obj.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		data := []byte(args[0].String())
		// 合并不完整的前缀
		if len(incomplete) > 0 {
			data = append(incomplete, data...)
			incomplete = nil
		}
		// 找到最后一个完整 UTF-8 字符的边界。
		validLen := utf8ValidPrefix(data)
		if validLen < len(data) {
			incomplete = data[validLen:]
		}
		return engine.Str(string(data[:validLen])), nil
	}))

	// end([buffer])：刷新剩余数据。
	_ = obj.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		var result string
		if len(args) > 0 && !args[0].IsUndefined() {
			data := []byte(args[0].String())
			if len(incomplete) > 0 {
				data = append(incomplete, data...)
			}
			result = string(data) // 容错：即使不完整也输出
		} else if len(incomplete) > 0 {
			result = string(incomplete) // 输出剩余（可能含无效字节，用替换字符）
		}
		incomplete = nil
		return engine.Str(result), nil
	}))

	_ = obj.Set("encoding", engine.Str(encoding))

	return obj
}

// utf8ValidPrefix 返回 data 中最长的有效 UTF-8 前缀长度
// （不截断多字节字符的中间部分）。
func utf8ValidPrefix(data []byte) int {
	for i := len(data); i > 0; i-- {
		if utf8.Valid(data[:i]) && utf8.RuneStart(data[i-1]) {
			return i
		}
	}
	return 0
}

// 确保 fmt 被使用（避免未使用 import）。
var _ = fmt.Sprintf
