//go:build amd64

package native

import (
	"fmt"
	"strings"

	"golang.org/x/arch/x86/x86asm"
)

func Disassemble(machineCode []byte) string {
	var out strings.Builder
	for offset := 0; offset < len(machineCode); {
		inst, err := x86asm.Decode(machineCode[offset:], 64)
		if err != nil || inst.Len <= 0 || offset+inst.Len > len(machineCode) {
			fmt.Fprintf(&out, "%04x  %-47s db 0x%02x\n", offset, fmt.Sprintf("%02x", machineCode[offset]), machineCode[offset])
			offset++
			continue
		}
		raw := fmt.Sprintf("% x", machineCode[offset:offset+inst.Len])
		fmt.Fprintf(&out, "%04x  %-47s %s\n", offset, raw, x86asm.IntelSyntax(inst, uint64(offset), nil))
		offset += inst.Len
	}
	return out.String()
}
