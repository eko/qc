//go:build arm64 && !purego

#include "textflag.h"

// Generated from the ARMv8 mnemonics in the comments: the Go assembler
// lacks most widening vector instructions, so they are written as their
// encodings. Registers: R0-R5 and V0-V31 only. The loops of 8-bit samples
// handle 16 of them per iteration, those of 16-bit samples 8, and
// accumulate into vector lanes (v30, v31) summed into 64 bits at the end.

// func sseNEON(a, b *byte, n int) uint64
//
// The sum of squared differences of n samples (a multiple of 16):
// |a - b| squared into 16-bit lanes.
TEXT ·sseNEON(SB), NOSPLIT|NOFRAME, $0-32
	MOVD	a+0(FP), R0
	MOVD	b+8(FP), R1
	MOVD	n+16(FP), R2
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0xb4000142 // cbz x2, sse_done
	// sse_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0x3cc10421 // ldr q1, [x1], #16
	WORD $0x6e217402 // uabd v2.16b, v0.16b, v1.16b
	WORD $0x2e22c043 // umull v3.8h, v2.8b, v2.8b
	WORD $0x6e22c044 // umull2 v4.8h, v2.16b, v2.16b
	WORD $0x6e60687e // uadalp v30.4s, v3.8h
	WORD $0x6e60689f // uadalp v31.4s, v4.8h
	WORD $0xf1004042 // subs x2, x2, #16
	WORD $0x54ffff01 // b.ne sse_loop
	// sse_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x6eb03be1 // uaddlv d1, v31.4s
	WORD $0x9e660003 // fmov x3, d0
	WORD $0x9e660024 // fmov x4, d1
	WORD $0x8b040063 // add x3, x3, x4
	MOVD	R3, ret+24(FP)
	RET

// func highpassNEON(up, cur, down *byte, n int) uint64
//
// The sum of |14·c − (l + r) − (v_l + 2·v_c + v_r)| over n positions (a
// multiple of 16) of cur, whose left and right neighbours are its samples
// before and after, with v the vertical 3-sums of up, cur and down. Both
// terms are non-negative 16-bit values (at most 3570): their absolute
// difference is uabd's.
TEXT ·highpassNEON(SB), NOSPLIT|NOFRAME, $0-40
	MOVD	up+0(FP), R0
	MOVD	cur+8(FP), R1
	MOVD	down+16(FP), R2
	MOVD	n+24(FP), R3
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0x4f00e5dd // movi v29.16b, #14
	WORD $0xb4000563 // cbz x3, hp_done
	// hp_loop:
	WORD $0x3dc00000 // ldr q0, [x0]
	WORD $0x3cc01001 // ldur q1, [x0, #1]
	WORD $0x3cc02002 // ldur q2, [x0, #2]
	WORD $0x3dc00023 // ldr q3, [x1]
	WORD $0x3cc01024 // ldur q4, [x1, #1]
	WORD $0x3cc02025 // ldur q5, [x1, #2]
	WORD $0x3dc00046 // ldr q6, [x2]
	WORD $0x3cc01047 // ldur q7, [x2, #1]
	WORD $0x3cc02048 // ldur q8, [x2, #2]
	WORD $0x91004000 // add x0, x0, #16
	WORD $0x91004021 // add x1, x1, #16
	WORD $0x91004042 // add x2, x2, #16
	WORD $0x2e26000a // uaddl v10.8h, v0.8b, v6.8b
	WORD $0x2e23114a // uaddw v10.8h, v10.8h, v3.8b
	WORD $0x2e27002b // uaddl v11.8h, v1.8b, v7.8b
	WORD $0x2e24116b // uaddw v11.8h, v11.8h, v4.8b
	WORD $0x2e28004c // uaddl v12.8h, v2.8b, v8.8b
	WORD $0x2e25118c // uaddw v12.8h, v12.8h, v5.8b
	WORD $0x4e6c854a // add v10.8h, v10.8h, v12.8h
	WORD $0x4e6b854a // add v10.8h, v10.8h, v11.8h
	WORD $0x4e6b854a // add v10.8h, v10.8h, v11.8h
	WORD $0x2e23114a // uaddw v10.8h, v10.8h, v3.8b
	WORD $0x2e25114a // uaddw v10.8h, v10.8h, v5.8b
	WORD $0x2e3dc08d // umull v13.8h, v4.8b, v29.8b
	WORD $0x6e6a75ad // uabd v13.8h, v13.8h, v10.8h
	WORD $0x6e6069be // uadalp v30.4s, v13.8h
	WORD $0x6e26000a // uaddl2 v10.8h, v0.16b, v6.16b
	WORD $0x6e23114a // uaddw2 v10.8h, v10.8h, v3.16b
	WORD $0x6e27002b // uaddl2 v11.8h, v1.16b, v7.16b
	WORD $0x6e24116b // uaddw2 v11.8h, v11.8h, v4.16b
	WORD $0x6e28004c // uaddl2 v12.8h, v2.16b, v8.16b
	WORD $0x6e25118c // uaddw2 v12.8h, v12.8h, v5.16b
	WORD $0x4e6c854a // add v10.8h, v10.8h, v12.8h
	WORD $0x4e6b854a // add v10.8h, v10.8h, v11.8h
	WORD $0x4e6b854a // add v10.8h, v10.8h, v11.8h
	WORD $0x6e23114a // uaddw2 v10.8h, v10.8h, v3.16b
	WORD $0x6e25114a // uaddw2 v10.8h, v10.8h, v5.16b
	WORD $0x6e3dc08d // umull2 v13.8h, v4.16b, v29.16b
	WORD $0x6e6a75ad // uabd v13.8h, v13.8h, v10.8h
	WORD $0x6e6069bf // uadalp v31.4s, v13.8h
	WORD $0xf1004063 // subs x3, x3, #16
	WORD $0x54fffae1 // b.ne hp_loop
	// hp_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x6eb03be1 // uaddlv d1, v31.4s
	WORD $0x9e660004 // fmov x4, d0
	WORD $0x9e660025 // fmov x5, d1
	WORD $0x8b050084 // add x4, x4, x5
	MOVD	R4, ret+32(FP)
	RET

// func firstOrderNEON(cur *byte, p1 *int16, n int) uint64
//
// The sum of |cur − p1| over n samples (a multiple of 16), then p1 = cur.
// 8-bit histories are 0–255: unsigned differences.
TEXT ·firstOrderNEON(SB), NOSPLIT|NOFRAME, $0-32
	MOVD	cur+0(FP), R0
	MOVD	p1+8(FP), R1
	MOVD	n+16(FP), R2
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0xb4000182 // cbz x2, t1_done
	// t1_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0xad400821 // ldp q1, q2, [x1]
	WORD $0x2f08a403 // uxtl v3.8h, v0.8b
	WORD $0x6f08a404 // uxtl2 v4.8h, v0.16b
	WORD $0x6e617465 // uabd v5.8h, v3.8h, v1.8h
	WORD $0x6e627486 // uabd v6.8h, v4.8h, v2.8h
	WORD $0x6e6068be // uadalp v30.4s, v5.8h
	WORD $0x6e6068df // uadalp v31.4s, v6.8h
	WORD $0xac811023 // stp q3, q4, [x1], #32
	WORD $0xf1004042 // subs x2, x2, #16
	WORD $0x54fffec1 // b.ne t1_loop
	// t1_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x6eb03be1 // uaddlv d1, v31.4s
	WORD $0x9e660003 // fmov x3, d0
	WORD $0x9e660024 // fmov x4, d1
	WORD $0x8b040063 // add x3, x3, x4
	MOVD	R3, ret+24(FP)
	RET

// func secondOrderNEON(cur *byte, p1, p2 *int16, n int) uint64
//
// The sum of |cur − 2·p1 + p2| = |(cur + p2) − 2·p1| over n samples (a
// multiple of 16), then p2 = p1 and p1 = cur.
TEXT ·secondOrderNEON(SB), NOSPLIT|NOFRAME, $0-40
	MOVD	cur+0(FP), R0
	MOVD	p1+8(FP), R1
	MOVD	p2+16(FP), R2
	MOVD	n+24(FP), R3
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0xb4000243 // cbz x3, t2_done
	// t2_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0xad400821 // ldp q1, q2, [x1]
	WORD $0xad401845 // ldp q5, q6, [x2]
	WORD $0x2f08a403 // uxtl v3.8h, v0.8b
	WORD $0x6f08a404 // uxtl2 v4.8h, v0.16b
	WORD $0x4e658467 // add v7.8h, v3.8h, v5.8h
	WORD $0x4e668488 // add v8.8h, v4.8h, v6.8h
	WORD $0x4e618429 // add v9.8h, v1.8h, v1.8h
	WORD $0x4e62844a // add v10.8h, v2.8h, v2.8h
	WORD $0x6e6974e7 // uabd v7.8h, v7.8h, v9.8h
	WORD $0x6e6a7508 // uabd v8.8h, v8.8h, v10.8h
	WORD $0x6e6068fe // uadalp v30.4s, v7.8h
	WORD $0x6e60691f // uadalp v31.4s, v8.8h
	WORD $0xac810841 // stp q1, q2, [x2], #32
	WORD $0xac811023 // stp q3, q4, [x1], #32
	WORD $0xf1004063 // subs x3, x3, #16
	WORD $0x54fffe01 // b.ne t2_loop
	// t2_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x6eb03be1 // uaddlv d1, v31.4s
	WORD $0x9e660004 // fmov x4, d0
	WORD $0x9e660025 // fmov x5, d1
	WORD $0x8b050084 // add x4, x4, x5
	MOVD	R4, ret+32(FP)
	RET

// func sse16NEON(a, b *int16, n int) uint64
//
// sseNEON on 16-bit samples (up to 12 bits), eight at a time: the squares
// are 32-bit, accumulated into 64-bit lanes.
TEXT ·sse16NEON(SB), NOSPLIT|NOFRAME, $0-32
	MOVD	a+0(FP), R0
	MOVD	b+8(FP), R1
	MOVD	n+16(FP), R2
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0xb4000142 // cbz x2, s16_done
	// s16_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0x3cc10421 // ldr q1, [x1], #16
	WORD $0x6e617402 // uabd v2.8h, v0.8h, v1.8h
	WORD $0x2e62c043 // umull v3.4s, v2.4h, v2.4h
	WORD $0x6e62c044 // umull2 v4.4s, v2.8h, v2.8h
	WORD $0x6ea0687e // uadalp v30.2d, v3.4s
	WORD $0x6ea0689f // uadalp v31.2d, v4.4s
	WORD $0xf1002042 // subs x2, x2, #8
	WORD $0x54ffff01 // b.ne s16_loop
	// s16_done:
	WORD $0x4eff87de // add v30.2d, v30.2d, v31.2d
	WORD $0x5ef1bbc0 // addp d0, v30.2d
	WORD $0x9e660003 // fmov x3, d0
	MOVD	R3, ret+24(FP)
	RET

// func highpass16NEON(up, cur, down *int16, n int) uint64
//
// highpassNEON on 16-bit samples (up to 12 bits: both terms stay below
// 2^16), eight positions at a time.
TEXT ·highpass16NEON(SB), NOSPLIT|NOFRAME, $0-40
	MOVD	up+0(FP), R0
	MOVD	cur+8(FP), R1
	MOVD	down+16(FP), R2
	MOVD	n+24(FP), R3
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x4f0085dd // movi v29.8h, #14
	WORD $0xb40003a3 // cbz x3, h16_done
	// h16_loop:
	WORD $0x3dc00000 // ldr q0, [x0]
	WORD $0x3cc02001 // ldur q1, [x0, #2]
	WORD $0x3cc04002 // ldur q2, [x0, #4]
	WORD $0x3dc00023 // ldr q3, [x1]
	WORD $0x3cc02024 // ldur q4, [x1, #2]
	WORD $0x3cc04025 // ldur q5, [x1, #4]
	WORD $0x3dc00046 // ldr q6, [x2]
	WORD $0x3cc02047 // ldur q7, [x2, #2]
	WORD $0x3cc04048 // ldur q8, [x2, #4]
	WORD $0x91004000 // add x0, x0, #16
	WORD $0x91004021 // add x1, x1, #16
	WORD $0x91004042 // add x2, x2, #16
	WORD $0x4e66840a // add v10.8h, v0.8h, v6.8h
	WORD $0x4e63854a // add v10.8h, v10.8h, v3.8h
	WORD $0x4e67842b // add v11.8h, v1.8h, v7.8h
	WORD $0x4e64856b // add v11.8h, v11.8h, v4.8h
	WORD $0x4e68844c // add v12.8h, v2.8h, v8.8h
	WORD $0x4e65858c // add v12.8h, v12.8h, v5.8h
	WORD $0x4e6c854a // add v10.8h, v10.8h, v12.8h
	WORD $0x4e6b854a // add v10.8h, v10.8h, v11.8h
	WORD $0x4e6b854a // add v10.8h, v10.8h, v11.8h
	WORD $0x4e63854a // add v10.8h, v10.8h, v3.8h
	WORD $0x4e65854a // add v10.8h, v10.8h, v5.8h
	WORD $0x4e7d9c8d // mul v13.8h, v4.8h, v29.8h
	WORD $0x6e6a75ad // uabd v13.8h, v13.8h, v10.8h
	WORD $0x6e6069be // uadalp v30.4s, v13.8h
	WORD $0xf1002063 // subs x3, x3, #8
	WORD $0x54fffca1 // b.ne h16_loop
	// h16_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x9e660004 // fmov x4, d0
	MOVD	R4, ret+32(FP)
	RET

// func firstOrder16NEON(cur, p1 *int16, n int) uint64
//
// firstOrderNEON on 16-bit samples, eight at a time.
TEXT ·firstOrder16NEON(SB), NOSPLIT|NOFRAME, $0-32
	MOVD	cur+0(FP), R0
	MOVD	p1+8(FP), R1
	MOVD	n+16(FP), R2
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0xb4000102 // cbz x2, f16_done
	// f16_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0x3dc00021 // ldr q1, [x1]
	WORD $0x6e617402 // uabd v2.8h, v0.8h, v1.8h
	WORD $0x6e60685e // uadalp v30.4s, v2.8h
	WORD $0x3c810420 // str q0, [x1], #16
	WORD $0xf1002042 // subs x2, x2, #8
	WORD $0x54ffff41 // b.ne f16_loop
	// f16_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x9e660003 // fmov x3, d0
	MOVD	R3, ret+24(FP)
	RET

// func secondOrder16NEON(cur, p1, p2 *int16, n int) uint64
//
// secondOrderNEON on 16-bit samples, eight at a time.
TEXT ·secondOrder16NEON(SB), NOSPLIT|NOFRAME, $0-40
	MOVD	cur+0(FP), R0
	MOVD	p1+8(FP), R1
	MOVD	p2+16(FP), R2
	MOVD	n+24(FP), R3
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0xb4000183 // cbz x3, g16_done
	// g16_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0x3dc00021 // ldr q1, [x1]
	WORD $0x3dc00042 // ldr q2, [x2]
	WORD $0x4e628403 // add v3.8h, v0.8h, v2.8h
	WORD $0x4e618424 // add v4.8h, v1.8h, v1.8h
	WORD $0x6e647463 // uabd v3.8h, v3.8h, v4.8h
	WORD $0x6e60687e // uadalp v30.4s, v3.8h
	WORD $0x3c810441 // str q1, [x2], #16
	WORD $0x3c810420 // str q0, [x1], #16
	WORD $0xf1002063 // subs x3, x3, #8
	WORD $0x54fffec1 // b.ne g16_loop
	// g16_done:
	WORD $0x6eb03bc0 // uaddlv d0, v30.4s
	WORD $0x9e660004 // fmov x4, d0
	MOVD	R4, ret+32(FP)
	RET
