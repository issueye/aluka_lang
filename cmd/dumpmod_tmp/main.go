package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/aluka-lang/aluka/internal/bundler/compile"
)

func main() {
	for _, path := range os.Args[1:] {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Println("err", path, err)
			continue
		}
		off, ln, _, ok := compile.ParseFooter(raw[len(raw)-compile.FooterSize:])
		if !ok {
			fmt.Println("no footer in", path)
			continue
		}
		if off+ln > uint64(len(raw)) {
			fmt.Println("bad payload range", path)
			continue
		}
		m, data, err := compile.ParsePayload(raw[off : off+ln])
		if err != nil {
			fmt.Println("parse err", path, err)
			continue
		}
		for _, e := range m.Modules {
			if e.Path == "config.ts" {
				fmt.Printf("CONFIG[%s] type=%s sourceKind=%s moduleKind=%s off=%d len=%d entry=%s\n", path, e.ModuleType, e.SourceKind, e.ModuleKind, e.Offset, e.Length, m.Entry)
				_ = data
			}
		}
		names := make([]string, len(m.Modules))
		for i, e := range m.Modules {
			names[i] = e.Path
		}
		sort.Strings(names)
		fmt.Printf("== %s (%d modules) ==\n", path, len(names))
		for _, n := range names {
			fmt.Println(n)
		}
	}
}
