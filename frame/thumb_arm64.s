//go:build arm64 && !purego

#include "textflag.h"

// Generated from the ARMv8 mnemonics in the comments: the Go assembler
// lacks the widening additions, so they are written as their encodings.
// Registers: R0-R2 and V0-V2 only.

// func addBytesNEON(dst *uint16, src *byte, n int)
//
// Adds n samples of src (a multiple of 16) to the 16-bit sums of dst.
TEXT ·addBytesNEON(SB), NOSPLIT|NOFRAME, $0-24
	MOVD	dst+0(FP), R0
	MOVD	src+8(FP), R1
	MOVD	n+16(FP), R2
	WORD $0xb4000102 // cbz x2, add_done
	// add_loop:
	WORD $0x3cc10420 // ldr q0, [x1], #16
	WORD $0x4c40a401 // ld1 {v1.8h, v2.8h}, [x0]
	WORD $0x2e201021 // uaddw v1.8h, v1.8h, v0.8b
	WORD $0x6e201042 // uaddw2 v2.8h, v2.8h, v0.16b
	WORD $0x4c9fa401 // st1 {v1.8h, v2.8h}, [x0], #32
	WORD $0xf1004042 // subs x2, x2, #16
	WORD $0x54ffff41 // b.ne add_loop
	// add_done:
	RET
