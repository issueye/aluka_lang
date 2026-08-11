//go:build amd64

#include "textflag.h"

TEXT ·callCode(SB), NOSPLIT, $0-24
	MOVQ entry+0(FP), AX
	MOVQ frame+8(FP), R10
	CALL AX
	MOVQ AX, ret+16(FP)
	RET
