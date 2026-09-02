package nodeutil

// node:wasi 内置模块——实验性（stability 1）的 WASI preview1 方法面。
//
// aluka 无 WebAssembly 运行时（ADR: docs/adr/WASI-WASM.md），因此 WASI 只提供
// 与 Node 22 一致的类/方法面：
//   - WASI(options)：校验 options（version/args/env/preopens/stdin/stdout/
//     stderr），错误码/消息与 Node 对齐（ERR_INVALID_ARG_TYPE /
//     ERR_INVALID_ARG_VALUE / ERR_OUT_OF_RANGE）
//   - instance.wasiImport：46 个 preview1 系统调用函数（调用前抛
//     ERR_WASI_NOT_STARTED，Node 语义）
//   - start(instance) / initialize(instance)：校验顺序与 Node 一致
//     （started 标记 → instance/exports/memory 校验 → _start/_initialize
//     校验），内存校验后真正执行 WASM 需要运行时，属非目标
//   - getImportObject()：{ wasi_snapshot_preview1 | wasi_unstable: wasiImport }
//
// 已知差异（knownDifference）：
//   - 无 WASM 运行时：start/initialize 无法真正执行 WASM；wasiImport 的
//     46 个函数在 start 后调用会抛错（Node 会在真实实例上执行系统调用）。
//   - Node 在模块加载时输出 ExperimentalWarning；aluka 不输出（避免污染
//     差分输出），实验地位由 ADR 与本文件注释标记。
//   - unstable 版本的函数名以 preview1 集合近似（Node 内部同名绑定）。

import (
	"fmt"
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
)

// wasiPreview1Functions 是 WASI preview1 导入对象的 46 个系统调用函数名。
var wasiPreview1Functions = []string{
	"args_get", "args_sizes_get", "clock_res_get", "clock_time_get",
	"environ_get", "environ_sizes_get", "fd_advise", "fd_allocate",
	"fd_close", "fd_datasync", "fd_fdstat_get", "fd_fdstat_set_flags",
	"fd_fdstat_set_rights", "fd_filestat_get", "fd_filestat_set_size",
	"fd_filestat_set_times", "fd_pread", "fd_prestat_dir_name", "fd_prestat_get",
	"fd_pwrite", "fd_read", "fd_readdir", "fd_renumber", "fd_seek", "fd_sync",
	"fd_tell", "fd_write", "path_create_directory", "path_filestat_get",
	"path_filestat_set_times", "path_link", "path_open", "path_readlink",
	"path_remove_directory", "path_rename", "path_symlink", "path_unlink_file",
	"poll_oneoff", "proc_exit", "proc_raise", "random_get", "sched_yield",
	"sock_accept", "sock_recv", "sock_send", "sock_shutdown",
}

// wasiCodeError 携带 Node 风格错误码（.code），name 按底层 error 决定
// （TypeError/RangeError/Error）。
type wasiCodeError struct {
	code string
	msg  string
	err  error
}

func (e *wasiCodeError) Error() string { return e.msg }
func (e *wasiCodeError) Code() string  { return e.code }
func (e *wasiCodeError) Unwrap() error { return e.err }

func wasiErrType(code, msg string) error {
	return &wasiCodeError{code: code, msg: msg, err: engine.ErrTypeError}
}

func wasiErrRange(code, msg string) error {
	return &wasiCodeError{code: code, msg: msg, err: engine.ErrRangeError}
}

func wasiErrPlain(code, msg string) error {
	return &wasiCodeError{code: code, msg: msg}
}

// wasiTypeString 渲染 Node 的 "Received ..." 片段。
func wasiTypeString(v engine.Value) string {
	switch v.Type() {
	case engine.TypeUndefined:
		return "undefined"
	case engine.TypeNull:
		return "null"
	case engine.TypeString:
		return "type string ('" + v.String() + "')"
	case engine.TypeNumber:
		return "type number (" + v.String() + ")"
	case engine.TypeBoolean:
		return "type boolean (" + v.String() + ")"
	case engine.TypeFunction:
		return "type function"
	default:
		return "type object"
	}
}

// wasiInstance 封装 WASI 实例状态。
type wasiInstance struct {
	started     bool
	bindingName string
	wasiImport  engine.Object
}

// NewWASI 构造 node:wasi 模块导出对象（{ WASI }）。
func NewWASI(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	proto := engine.NewObject()
	ctor := engine.NewFunction("WASI", func(args []engine.Value) (engine.Value, error) {
		options := engine.Undefined()
		if len(args) > 0 {
			options = args[0]
		}
		inst, err := newWASIInstance(ctx, options)
		if err != nil {
			return engine.Undefined(), err
		}
		// 实例原型链接到 WASI.prototype（支持 instanceof）。
		engine.SetProto(inst, proto)
		return inst, nil
	})
	ctorObj, _ := ctor.AsObject()
	_ = proto.Set("constructor", ctor)
	_ = ctorObj.Set("prototype", proto)
	_ = m.Set("WASI", ctor)

	return m, nil
}

// newWASIInstance 构造一个 WASI 实例（构造函数主体）。
func newWASIInstance(ctx engine.Context, options engine.Value) (engine.Value, error) {
	opts := engine.NewObject()
	if !options.IsUndefined() && !options.IsNull() {
		o, ok := options.AsObject()
		if !ok {
			return engine.Undefined(), wasiErrType("ERR_INVALID_ARG_TYPE",
				fmt.Sprintf("The \"options\" argument must be of type object. Received %s", wasiTypeString(options)))
		}
		opts = o
	}

	// options.version：必填字符串。
	verV, _ := opts.Get("version")
	if verV.Type() != engine.TypeString {
		return engine.Undefined(), wasiErrType("ERR_INVALID_ARG_TYPE",
			fmt.Sprintf("The \"options.version\" property must be of type string. Received %s", wasiTypeString(verV)))
	}
	version := verV.String()
	var bindingName string
	switch version {
	case "unstable":
		bindingName = "wasi_unstable"
	case "preview1":
		bindingName = "wasi_snapshot_preview1"
	default:
		return engine.Undefined(), wasiErrType("ERR_INVALID_ARG_VALUE",
			fmt.Sprintf("The property 'options.version' unsupported WASI version. Received '%s'", version))
	}

	// options.args：数组。
	if av, err := opts.Get("args"); err == nil && !av.IsUndefined() {
		if _, ok := av.(*engine.ArrayValue); !ok {
			return engine.Undefined(), wasiErrType("ERR_INVALID_ARG_TYPE",
				fmt.Sprintf("The \"options.args\" property must be an instance of Array. Received %s", wasiTypeString(av)))
		}
	}
	// options.env / options.preopens：对象。
	for _, k := range []string{"env", "preopens"} {
		if ev, err := opts.Get(k); err == nil && !ev.IsUndefined() {
			if _, ok := ev.AsObject(); !ok {
				return engine.Undefined(), wasiErrType("ERR_INVALID_ARG_TYPE",
					fmt.Sprintf("The \"options.%s\" property must be of type object. Received %s", k, wasiTypeString(ev)))
			}
		}
	}
	// options.stdin/stdout/stderr：0..2^31-1 整数。
	for _, k := range []string{"stdin", "stdout", "stderr"} {
		v, _ := opts.Get(k)
		if v.IsUndefined() {
			continue
		}
		n, ok := v.Int()
		if !ok || n < 0 || n > math.MaxInt32 {
			received := n
			if !ok {
				received = -1
			}
			return engine.Undefined(), wasiErrRange("ERR_OUT_OF_RANGE",
				fmt.Sprintf("The value of \"options.%s\" is out of range. It must be >= 0 && <= 2147483647. Received %d", k, received))
		}
	}

	// wasiImport：46 个系统调用函数。Node 中 start() 成功（kInstance 设置）
	// 前调用一律抛 ERR_WASI_NOT_STARTED；aluka 无 WASM 运行时永远无法成功
	// start，故始终抛该错误（与 Node 可达状态一致）。
	wasiImport := engine.NewObject()
	for _, name := range wasiPreview1Functions {
		_ = wasiImport.Set(name, engine.NewFunction(name, func(a []engine.Value) (engine.Value, error) {
			return engine.Undefined(), wasiErrPlain("ERR_WASI_NOT_STARTED", "wasi.start() has not been called")
		}))
	}
	inst := &wasiInstance{
		started:     false,
		bindingName: bindingName,
		wasiImport:  wasiImport,
	}

	obj := engine.NewObject()
	_ = obj.Set("wasiImport", wasiImport)

	// start(instance)：started 标记 → instance/exports/memory → _start/_initialize。
	_ = obj.Set("start", engine.NewFunction("start", func(args []engine.Value) (engine.Value, error) {
		if inst.started {
			return engine.Undefined(), wasiErrPlain("ERR_WASI_ALREADY_STARTED", "WASI instance has already started")
		}
		inst.started = true
		if err := wasiSetupInstance(ctx, args); err != nil {
			return engine.Undefined(), err
		}
		// 无 WASM 运行时：即使内存校验通过也无法执行 _start。
		return engine.Undefined(), wasiErrPlain("ERR_WASI_NOT_IMPLEMENTED",
			"aluka: WASI _start requires a WebAssembly runtime (see docs/adr/WASI-WASM.md)")
	}))

	// initialize(instance)：started 标记 → instance/exports/memory → _initialize。
	_ = obj.Set("initialize", engine.NewFunction("initialize", func(args []engine.Value) (engine.Value, error) {
		if inst.started {
			return engine.Undefined(), wasiErrPlain("ERR_WASI_ALREADY_STARTED", "WASI instance has already started")
		}
		inst.started = true
		if err := wasiSetupInstance(ctx, args); err != nil {
			return engine.Undefined(), err
		}
		return engine.Undefined(), wasiErrPlain("ERR_WASI_NOT_IMPLEMENTED",
			"aluka: WASI _initialize requires a WebAssembly runtime (see docs/adr/WASI-WASM.md)")
	}))

	// getImportObject()：{ [bindingName]: wasiImport }。
	_ = obj.Set("getImportObject", engine.NewFunction("getImportObject", func(args []engine.Value) (engine.Value, error) {
		io := engine.NewObject()
		_ = io.Set(inst.bindingName, wasiImport)
		return io, nil
	}))

	return obj, nil
}

// wasiSetupInstance 执行 Node setupInstance 的校验：instance/exports 为对象、
// exports.memory 为 WebAssembly.Memory（aluka 无 Memory 类，任何内存值都
// 无法通过——以错误路径对齐 Node）。
func wasiSetupInstance(ctx engine.Context, args []engine.Value) error {
	instance := engine.Undefined()
	if len(args) > 0 {
		instance = args[0]
	}
	if _, ok := instance.AsObject(); !ok {
		return wasiErrType("ERR_INVALID_ARG_TYPE",
			fmt.Sprintf("The \"instance\" argument must be of type object. Received %s", wasiTypeString(instance)))
	}
	exports, _ := instance.AsObject()
	exportsVal, _ := exports.Get("exports")
	if _, ok := exportsVal.AsObject(); !ok {
		return wasiErrType("ERR_INVALID_ARG_TYPE",
			fmt.Sprintf("The \"instance.exports\" property must be of type object. Received %s", wasiTypeString(exportsVal)))
	}
	memory, _ := exportsVal.AsObject()
	_, _ = memory.Get("memory")
	// WebAssembly.Memory 类在 aluka 不存在 → 无法通过校验（Node 需要
	// instanceof WebAssembly.Memory）。
	return wasiErrType("ERR_INVALID_ARG_TYPE",
		"\"instance.exports.memory\" property must be a WebAssembly.Memory object")
}
