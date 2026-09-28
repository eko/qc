//go:build arm64 && !purego

#include "textflag.h"

// Generated from the ARMv8 mnemonics in the comments: the Go assembler
// lacks most widening vector instructions, so they are written as their
// encodings. Registers: R0-R5 and V0-V31 only.

// func statsNEON(p *byte, n int, black, white byte, s *rowStats)
//
// Accumulates into s the sum, minimum and maximum of n samples (a multiple
// of 16, at most maxStatsSamples: the out-of-range counters are 8-bit
// lanes incremented at most once per 16 samples) and how many are below
// black or above white.
TEXT ·statsNEON(SB), NOSPLIT|NOFRAME, $0-32
	MOVD	p+0(FP), R0
	MOVD	n+8(FP), R1
	MOVBU	black+16(FP), R2
	MOVBU	white+17(FP), R3
	MOVD	s+24(FP), R4
	WORD $0x4e010c54 // dup v20.16b, w2
	WORD $0x4e010c75 // dup v21.16b, w3
	WORD $0x6f07e7f6 // movi v22.2d, #0xffffffffffffffff
	WORD $0x6f00e417 // movi v23.2d, #0
	WORD $0x6f00e418 // movi v24.2d, #0
	WORD $0x6f00e419 // movi v25.2d, #0
	WORD $0x6f00e41a // movi v26.2d, #0
	WORD $0xb4000181 // cbz x1, stats_done
	// stats_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0x6e206ed6 // umin v22.16b, v22.16b, v0.16b
	WORD $0x6e2066f7 // umax v23.16b, v23.16b, v0.16b
	// Comparisons set lanes to -1: subtracting them counts.
	WORD $0x6e203681 // cmhi v1.16b, v20.16b, v0.16b
	WORD $0x6e353402 // cmhi v2.16b, v0.16b, v21.16b
	WORD $0x6e218718 // sub v24.16b, v24.16b, v1.16b
	WORD $0x6e228739 // sub v25.16b, v25.16b, v2.16b
	WORD $0x6e202803 // uaddlp v3.8h, v0.16b
	WORD $0x6e60687a // uadalp v26.4s, v3.8h
	WORD $0xf1004021 // subs x1, x1, #16
	WORD $0x54fffec1 // b.ne stats_loop
	// stats_done:
	WORD $0x6e31aad6 // uminv b22, v22.16b
	WORD $0x6e30aaf7 // umaxv b23, v23.16b
	WORD $0x6e303b18 // uaddlv h24, v24.16b
	WORD $0x6e303b39 // uaddlv h25, v25.16b
	WORD $0x6eb03b5a // uaddlv d26, v26.4s
	// s.sum += sum; s.below += below; s.above += above; s.lo = min; s.hi = max
	WORD $0xa9401885 // ldp x5, x6, [x4]
	WORD $0x9e660342 // fmov x2, d26
	WORD $0x8b0200a5 // add x5, x5, x2
	WORD $0x0e023f02 // umov w2, v24.h[0]
	WORD $0x8b0200c6 // add x6, x6, x2
	WORD $0xa9001885 // stp x5, x6, [x4]
	WORD $0xf9400885 // ldr x5, [x4, #16]
	WORD $0x0e023f22 // umov w2, v25.h[0]
	WORD $0x8b0200a5 // add x5, x5, x2
	WORD $0xf9000885 // str x5, [x4, #16]
	WORD $0x39406085 // ldrb w5, [x4, #24]
	WORD $0x0e013ec2 // umov w2, v22.b[0]
	WORD $0x6b05005f // cmp w2, w5
	WORD $0x1a853045 // csel w5, w2, w5, lo
	WORD $0x39006085 // strb w5, [x4, #24]
	WORD $0x39406485 // ldrb w5, [x4, #25]
	WORD $0x0e013ee2 // umov w2, v23.b[0]
	WORD $0x6b05005f // cmp w2, w5
	WORD $0x1a858045 // csel w5, w2, w5, hi
	WORD $0x39006485 // strb w5, [x4, #25]
	RET
