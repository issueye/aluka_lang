//go:build !amd64

package native

import (
	"fmt"
	"strings"
)

func Disassemble(machineCode []byte) string {
	var out strings.Builder
	for offset := 0; offset < len(machineCode); offset += 16 {
		end := offset + 16
		if end > len(machineCode) {
			end = len(machineCode)
		}
		fmt.Fprintf(&out, "%04x  % x\n", offset, machineCode[offset:end])
	}
	return out.String()
}
