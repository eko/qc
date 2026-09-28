//go:build !purego

#include "textflag.h"

// Generated from the ARMv8 mnemonics in the comments: the Go assembler
// lacks most widening and floating-point vector instructions, so they are
// written as their encodings. Registers: R0-R5 and V0-V31 only.

// func sobelSquaresNEON(above, row, below *byte, out *int32, n int) int64
//
// Writes gx² + gy² for n pixels (a multiple of 16) and returns their sum.
// Each iteration reads 18 samples of each row and writes 16 values: v are
// the vertically smoothed columns (a + 2r + b) and d the vertical
// differences (b - a) as 16-bit lanes, gx = v[x+2] - v[x] and
// gy = d[x] + 2d[x+1] + d[x+2], squared into 32-bit lanes.
TEXT ·sobelSquaresNEON(SB), NOSPLIT|NOFRAME, $0-48
	MOVD	above+0(FP), R0
	MOVD	row+8(FP), R1
	MOVD	below+16(FP), R2
	MOVD	out+24(FP), R3
	MOVD	n+32(FP), R4
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0xb40006a4 // cbz x4, sobel_done
	// sobel_loop:
	WORD $0x3dc00000 // ldr q0, [x0]
	WORD $0x3cc01001 // ldur q1, [x0, #1]
	WORD $0x3cc02002 // ldur q2, [x0, #2]
	WORD $0x3dc00023 // ldr q3, [x1]
	WORD $0x3cc02025 // ldur q5, [x1, #2]
	WORD $0x3dc00046 // ldr q6, [x2]
	WORD $0x3cc01047 // ldur q7, [x2, #1]
	WORD $0x3cc02048 // ldur q8, [x2, #2]
	WORD $0x91004000 // add x0, x0, #16
	WORD $0x91004021 // add x1, x1, #16
	WORD $0x91004042 // add x2, x2, #16
	// v[x] = a0 + b0 + 2 r0
	WORD $0x2e260010 // uaddl v16.8h, v0.8b, v6.8b
	WORD $0x6e260011 // uaddl2 v17.8h, v0.16b, v6.16b
	WORD $0x2f09a472 // ushll v18.8h, v3.8b, #1
	WORD $0x6f09a473 // ushll2 v19.8h, v3.16b, #1
	WORD $0x4e728610 // add v16.8h, v16.8h, v18.8h
	WORD $0x4e738631 // add v17.8h, v17.8h, v19.8h
	// v[x+2] = a2 + b2 + 2 r2
	WORD $0x2e280052 // uaddl v18.8h, v2.8b, v8.8b
	WORD $0x6e280053 // uaddl2 v19.8h, v2.16b, v8.16b
	WORD $0x2f09a4b4 // ushll v20.8h, v5.8b, #1
	WORD $0x6f09a4b5 // ushll2 v21.8h, v5.16b, #1
	WORD $0x4e748652 // add v18.8h, v18.8h, v20.8h
	WORD $0x4e758673 // add v19.8h, v19.8h, v21.8h
	// gx = v[x+2] - v[x]
	WORD $0x6e708650 // sub v16.8h, v18.8h, v16.8h
	WORD $0x6e718671 // sub v17.8h, v19.8h, v17.8h
	// d[x], d[x+1], d[x+2]
	WORD $0x2e2020d2 // usubl v18.8h, v6.8b, v0.8b
	WORD $0x6e2020d3 // usubl2 v19.8h, v6.16b, v0.16b
	WORD $0x2e2120f4 // usubl v20.8h, v7.8b, v1.8b
	WORD $0x6e2120f5 // usubl2 v21.8h, v7.16b, v1.16b
	WORD $0x2e222116 // usubl v22.8h, v8.8b, v2.8b
	WORD $0x6e222117 // usubl2 v23.8h, v8.16b, v2.16b
	// gy = d[x] + d[x+2] + 2 d[x+1]
	WORD $0x4e768652 // add v18.8h, v18.8h, v22.8h
	WORD $0x4e778673 // add v19.8h, v19.8h, v23.8h
	WORD $0x4f115694 // shl v20.8h, v20.8h, #1
	WORD $0x4f1156b5 // shl v21.8h, v21.8h, #1
	WORD $0x4e748652 // add v18.8h, v18.8h, v20.8h
	WORD $0x4e758673 // add v19.8h, v19.8h, v21.8h
	// gx² + gy²
	WORD $0x0e70c218 // smull v24.4s, v16.4h, v16.4h
	WORD $0x4e70c219 // smull2 v25.4s, v16.8h, v16.8h
	WORD $0x0e71c23a // smull v26.4s, v17.4h, v17.4h
	WORD $0x4e71c23b // smull2 v27.4s, v17.8h, v17.8h
	WORD $0x0e728258 // smlal v24.4s, v18.4h, v18.4h
	WORD $0x4e728259 // smlal2 v25.4s, v18.8h, v18.8h
	WORD $0x0e73827a // smlal v26.4s, v19.4h, v19.4h
	WORD $0x4e73827b // smlal2 v27.4s, v19.8h, v19.8h
	WORD $0x4c9f2878 // st1 {v24.4s, v25.4s, v26.4s, v27.4s}, [x3], #64
	WORD $0x6ea06b1e // uadalp v30.2d, v24.4s
	WORD $0x6ea06b3f // uadalp v31.2d, v25.4s
	WORD $0x6ea06b5e // uadalp v30.2d, v26.4s
	WORD $0x6ea06b7f // uadalp v31.2d, v27.4s
	WORD $0xf1004084 // subs x4, x4, #16
	WORD $0x54fff9a1 // b.ne sobel_loop
	// sobel_done:
	WORD $0x4eff87de // add v30.2d, v30.2d, v31.2d
	WORD $0x5ef1bbde // addp d30, v30.2d
	WORD $0x9e6603c0 // fmov x0, d30
	MOVD	R0, ret+40(FP)
	RET

// func magnitudeSumsNEON(q0, q1, q2, q3 *int32, n int, sums *[4]float64)
//
// Sets sums[r] to the sum of the square roots of the n values of row r (n
// a multiple of 4), added in order: each row is a lane of an accumulator,
// so the additions of a row stay a serial chain, as in a scalar loop.
TEXT ·magnitudeSumsNEON(SB), NOSPLIT|NOFRAME, $0-48
	MOVD	q0+0(FP), R0
	MOVD	q1+8(FP), R1
	MOVD	q2+16(FP), R2
	MOVD	q3+24(FP), R3
	MOVD	n+32(FP), R4
	MOVD	sums+40(FP), R5
	WORD $0x6f00e41c // movi v28.2d, #0
	WORD $0x6f00e41d // movi v29.2d, #0
	WORD $0xb4000564 // cbz x4, sums_done
	// sums_loop:
	WORD $0x4cdf7800 // ld1 {v0.4s}, [x0], #16
	WORD $0x4cdf7821 // ld1 {v1.4s}, [x1], #16
	WORD $0x4cdf7842 // ld1 {v2.4s}, [x2], #16
	WORD $0x4cdf7863 // ld1 {v3.4s}, [x3], #16
	// Lanes (row 0, row 1) and (row 2, row 3) of 4 consecutive pixels.
	WORD $0x4e813804 // zip1 v4.4s, v0.4s, v1.4s
	WORD $0x4e817805 // zip2 v5.4s, v0.4s, v1.4s
	WORD $0x4e833846 // zip1 v6.4s, v2.4s, v3.4s
	WORD $0x4e837847 // zip2 v7.4s, v2.4s, v3.4s
	WORD $0x0f20a490 // sxtl v16.2d, v4.2s
	WORD $0x4f20a491 // sxtl2 v17.2d, v4.4s
	WORD $0x0f20a4b2 // sxtl v18.2d, v5.2s
	WORD $0x4f20a4b3 // sxtl2 v19.2d, v5.4s
	WORD $0x0f20a4d4 // sxtl v20.2d, v6.2s
	WORD $0x4f20a4d5 // sxtl2 v21.2d, v6.4s
	WORD $0x0f20a4f6 // sxtl v22.2d, v7.2s
	WORD $0x4f20a4f7 // sxtl2 v23.2d, v7.4s
	WORD $0x4e61da10 // scvtf v16.2d, v16.2d
	WORD $0x4e61da31 // scvtf v17.2d, v17.2d
	WORD $0x4e61da52 // scvtf v18.2d, v18.2d
	WORD $0x4e61da73 // scvtf v19.2d, v19.2d
	WORD $0x4e61da94 // scvtf v20.2d, v20.2d
	WORD $0x4e61dab5 // scvtf v21.2d, v21.2d
	WORD $0x4e61dad6 // scvtf v22.2d, v22.2d
	WORD $0x4e61daf7 // scvtf v23.2d, v23.2d
	WORD $0x6ee1fa10 // fsqrt v16.2d, v16.2d
	WORD $0x6ee1fa94 // fsqrt v20.2d, v20.2d
	WORD $0x6ee1fa31 // fsqrt v17.2d, v17.2d
	WORD $0x6ee1fab5 // fsqrt v21.2d, v21.2d
	WORD $0x6ee1fa52 // fsqrt v18.2d, v18.2d
	WORD $0x6ee1fad6 // fsqrt v22.2d, v22.2d
	WORD $0x6ee1fa73 // fsqrt v19.2d, v19.2d
	WORD $0x6ee1faf7 // fsqrt v23.2d, v23.2d
	WORD $0x4e70d79c // fadd v28.2d, v28.2d, v16.2d
	WORD $0x4e74d7bd // fadd v29.2d, v29.2d, v20.2d
	WORD $0x4e71d79c // fadd v28.2d, v28.2d, v17.2d
	WORD $0x4e75d7bd // fadd v29.2d, v29.2d, v21.2d
	WORD $0x4e72d79c // fadd v28.2d, v28.2d, v18.2d
	WORD $0x4e76d7bd // fadd v29.2d, v29.2d, v22.2d
	WORD $0x4e73d79c // fadd v28.2d, v28.2d, v19.2d
	WORD $0x4e77d7bd // fadd v29.2d, v29.2d, v23.2d
	WORD $0xf1001084 // subs x4, x4, #4
	WORD $0x54fffae1 // b.ne sums_loop
	// sums_done:
	WORD $0x4c00acbc // st1 {v28.2d, v29.2d}, [x5]
	RET

// func diffMomentsNEON(a, b *byte, n int) (sum, sumSq int64)
//
// Returns the sum and the sum of squares of b - a over n samples (a
// multiple of 16, at most maxMomentSamples: each 32-bit lane of the
// squares' accumulators receives one square per 16 samples).
TEXT ·diffMomentsNEON(SB), NOSPLIT|NOFRAME, $0-40
	MOVD	a+0(FP), R0
	MOVD	b+8(FP), R1
	MOVD	n+16(FP), R2
	WORD $0x6f00e41a // movi v26.2d, #0
	WORD $0x6f00e41b // movi v27.2d, #0
	WORD $0x6f00e41c // movi v28.2d, #0
	WORD $0x6f00e41d // movi v29.2d, #0
	WORD $0x6f00e41e // movi v30.2d, #0
	WORD $0x6f00e41f // movi v31.2d, #0
	WORD $0xb40001a2 // cbz x2, moments_done
	// moments_loop:
	WORD $0x3cc10400 // ldr q0, [x0], #16
	WORD $0x3cc10421 // ldr q1, [x1], #16
	WORD $0x2e202022 // usubl v2.8h, v1.8b, v0.8b
	WORD $0x6e202023 // usubl2 v3.8h, v1.16b, v0.16b
	WORD $0x4e60685a // sadalp v26.4s, v2.8h
	WORD $0x4e60687b // sadalp v27.4s, v3.8h
	WORD $0x0e62805c // smlal v28.4s, v2.4h, v2.4h
	WORD $0x4e62805d // smlal2 v29.4s, v2.8h, v2.8h
	WORD $0x0e63807e // smlal v30.4s, v3.4h, v3.4h
	WORD $0x4e63807f // smlal2 v31.4s, v3.8h, v3.8h
	WORD $0xf1004042 // subs x2, x2, #16
	WORD $0x54fffea1 // b.ne moments_loop
	// moments_done:
	WORD $0x4ebb875a // add v26.4s, v26.4s, v27.4s
	WORD $0x4eb03b5a // saddlv d26, v26.4s
	WORD $0x9e660340 // fmov x0, d26
	WORD $0x4ebd879c // add v28.4s, v28.4s, v29.4s
	WORD $0x4ebf87de // add v30.4s, v30.4s, v31.4s
	WORD $0x6eb03b9c // uaddlv d28, v28.4s
	WORD $0x6eb03bde // uaddlv d30, v30.4s
	WORD $0x9e660381 // fmov x1, d28
	WORD $0x9e6603c2 // fmov x2, d30
	WORD $0x8b020021 // add x1, x1, x2
	MOVD	R0, sum+24(FP)
	MOVD	R1, sumSq+32(FP)
	RET
