package main

import (
	"fmt"
	"os"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

func main() {
	src, _ := os.ReadFile(os.Args[1])
	prog, err := parser.ParseModule(string(src))
	if err != nil {
		fmt.Println("parse err:", err)
		return
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		fmt.Println("vm err:", err)
		return
	}
	transformed := module.TransformESMToCJS(prog, os.Args[1])
	prog2 := module.WrapESMAST(transformed, os.Args[1])
	mod, err := vm.CompileAST(prog2, os.Args[1])
	if err != nil {
		fmt.Println("compile err:", err)
		return
	}
	for i, fn := range mod.Functions {
		for j, cst := range fn.Constants {
			switch cst.Type() {
			case 0, 1, 2, 3, 4, 5:
			default:
				fmt.Printf("fn[%d] %q const[%d] type=%v val=%v\n", i, fn.Name, j, cst.Type(), cst)
			}
		}
	}
	fmt.Println("scan done")
}
