//go:build darwin && amd64

#include "textflag.h"

TEXT ·abiCall0(SB), NOSPLIT, $8-16
	MOVQ fn+0(FP), AX
	CALL AX
	MOVQ AX, ret+8(FP)
	RET

TEXT ·abiCall1(SB), NOSPLIT, $8-24
	MOVQ fn+0(FP), AX
	MOVQ a1+8(FP), DI
	CALL AX
	MOVQ AX, ret+16(FP)
	RET

TEXT ·objcMsgSend(SB), NOSPLIT, $8-56
	MOVQ obj+0(FP), DI
	MOVQ sel+8(FP), SI
	MOVQ a1+16(FP), DX
	MOVQ a2+24(FP), CX
	MOVQ a3+32(FP), R8
	MOVQ a4+40(FP), R9
	MOVQ ·objcSendPtr(SB), AX
	CALL AX
	MOVQ AX, ret+48(FP)
	RET

TEXT ·objcMsgSendF1(SB), NOSPLIT, $8-32
	MOVQ obj+0(FP), DI
	MOVQ sel+8(FP), SI
	MOVSD f+16(FP), X0
	MOVQ ·objcSendPtr(SB), AX
	CALL AX
	MOVQ AX, ret+24(FP)
	RET

TEXT ·objcMsgSendRect(SB), NOSPLIT, $8-80
	MOVQ obj+0(FP), DI
	MOVQ sel+8(FP), SI
	MOVSD x+16(FP), X0
	MOVSD y+24(FP), X1
	MOVSD w+32(FP), X2
	MOVSD h+40(FP), X3
	MOVQ a1+48(FP), DX
	MOVQ a2+56(FP), CX
	MOVQ a3+64(FP), R8
	MOVQ ·objcSendPtr(SB), AX
	CALL AX
	MOVQ AX, ret+72(FP)
	RET
