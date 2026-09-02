// 带错误码的文件系统错误载体（fs.cp/rmdir、tty 初始化）。

package nodebase

// FSCodeError 带错误码的普通 Error（非 TypeError；fs.cp/rmdir、tty 初始化用）。
// 字段保持未导出，构造走 NewFSCodeError——避免各领域包直写字段。
type FSCodeError struct {
	code string
	msg  string
}

// NewFSCodeError 构造带 Node 错误码的 fs 错误。code 可为空串（仅消息）。
func NewFSCodeError(code, msg string) *FSCodeError {
	return &FSCodeError{code: code, msg: msg}
}

func (e *FSCodeError) Error() string { return e.msg }

func (e *FSCodeError) Code() string { return e.code }
