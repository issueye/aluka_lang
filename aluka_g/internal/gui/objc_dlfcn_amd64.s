//go:build darwin && amd64

#include "textflag.h"

TEXT ·dlopenABI(SB), NOSPLIT, $8-24
	MOVQ path+0(FP), DI
	MOVQ mode+8(FP), SI
	CALL libc_dlopen(SB)
	MOVQ AX, ret+16(FP)
	RET

TEXT ·dlsymABI(SB), NOSPLIT, $8-24
	MOVQ handle+0(FP), DI
	MOVQ symbol+8(FP), SI
	CALL libc_dlsym(SB)
	MOVQ AX, ret+16(FP)
	RET

TEXT ·dlerrorABI(SB), NOSPLIT, $8-8
	CALL libc_dlerror(SB)
	MOVQ AX, ret+0(FP)
	RET
