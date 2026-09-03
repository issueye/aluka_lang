//go:build darwin && arm64

#include "textflag.h"

TEXT ·abiCall0(SB), NOSPLIT, $0-16
	MOVD fn+0(FP), R8
	CALL R8
	MOVD R0, ret+8(FP)
	RET

TEXT ·abiCall1(SB), NOSPLIT, $0-24
	MOVD fn+0(FP), R8
	MOVD a1+8(FP), R0
	CALL R8
	MOVD R0, ret+16(FP)
	RET

TEXT ·objcMsgSend(SB), NOSPLIT, $0-56
	MOVD obj+0(FP), R0
	MOVD sel+8(FP), R1
	MOVD a1+16(FP), R2
	MOVD a2+24(FP), R3
	MOVD a3+32(FP), R4
	MOVD a4+40(FP), R5
	MOVD ·objcSendPtr(SB), R6
	CALL R6
	MOVD R0, ret+48(FP)
	RET

TEXT ·objcMsgSendF1(SB), NOSPLIT, $0-32
	MOVD obj+0(FP), R0
	MOVD sel+8(FP), R1
	FMOVD f+16(FP), F0
	MOVD ·objcSendPtr(SB), R6
	CALL R6
	MOVD R0, ret+24(FP)
	RET

TEXT ·objcMsgSendRect(SB), NOSPLIT, $0-80
	MOVD obj+0(FP), R0
	MOVD sel+8(FP), R1
	FMOVD x+16(FP), F0
	FMOVD y+24(FP), F1
	FMOVD w+32(FP), F2
	FMOVD h+40(FP), F3
	MOVD a1+48(FP), R2
	MOVD a2+56(FP), R3
	MOVD a3+64(FP), R4
	MOVD ·objcSendPtr(SB), R6
	CALL R6
	MOVD R0, ret+72(FP)
	RET
