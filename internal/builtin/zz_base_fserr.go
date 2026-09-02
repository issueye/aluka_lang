// 跨模块共享 helper（分包基座）。

package builtin

// fsCodeError 带错误码的普通 Error（非 TypeError；cp/cpSync 用）。
type fsCodeError struct {
	code string
	msg  string
}

func (e *fsCodeError) Error() string { return e.msg }

func (e *fsCodeError) Code() string { return e.code }
