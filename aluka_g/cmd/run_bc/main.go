package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gconsole"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "用法: run_bc <file.bc>\n")
		os.Exit(1)
	}

	bcPath := os.Args[1]
	data, err := os.ReadFile(bcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取字节码失败: %v\n", err)
		os.Exit(1)
	}

	mod, err := bytecode.Deserialize(bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "反序列化字节码失败: %v\n", err)
		os.Exit(1)
	}

	vm, err := interpreter.NewVM()
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 VM 失败: %v\n", err)
		os.Exit(1)
	}

	// 注册全局 console 与全局对象
	_ = gconsole.NewConsole(vm, gconsole.ConsoleConfig{})
	_ = vm.Global().Set("globalThis", vm.Global())
	_ = vm.Global().Set("global", vm.Global())

	val, err := vm.RunModule(mod)
	if err != nil {
		fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
		os.Exit(1)
	}

	// 若顶层返回的是包装闭包（如 Go 编译产物），执行闭包调用进入用户代码
	if val != nil {
		if fn, ok := val.AsFunction(); ok {
			_, err = fn.Call(nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "调用闭包失败: %v\n", err)
				os.Exit(1)
			}
		}
	}
}
