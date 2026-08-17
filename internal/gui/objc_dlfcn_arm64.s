//go:build darwin && arm64

#include "textflag.h"

TEXT ·dlopenABI(SB), NOSPLIT, $0-24
	MOVD path+0(FP), R0
	MOVD mode+8(FP), R1
	CALL libc_dlopen(SB)
	MOVD R0, ret+16(FP)
	RET

TEXT ·dlsymABI(SB), NOSPLIT, $0-24
	MOVD handle+0(FP), R0
	MOVD symbol+8(FP), R1
	CALL libc_dlsym(SB)
	MOVD R0, ret+16(FP)
	RET

TEXT ·dlerrorABI(SB), NOSPLIT, $0-8
	CALL libc_dlerror(SB)
	MOVD R0, ret+0(FP)
	RET
