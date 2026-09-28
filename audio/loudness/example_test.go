package loudness_test

import (
	"fmt"
	"math"

	"github.com/eko/qc/audio/loudness"
)

// A 1 kHz sine at -23 dBFS on both channels of a stereo pair reads
// -23 LUFS: the first case of EBU Tech 3341.
func ExampleMeter() {
	const rate = 48000

	tone := make([]float32, 20*rate)
	for i := range tone {
		tone[i] = float32(math.Pow(10, -23.0/20) * math.Sin(2*math.Pi*1000*float64(i)/rate))
	}

	meter := loudness.NewMeter(rate, []float64{1, 1})
	if err := meter.Add([][]float32{tone, tone}); err != nil {
		fmt.Println(err)

		return
	}

	r := meter.Result()
	fmt.Printf("%.1f LUFS, LRA %.1f LU, true peak %.1f dBTP\n", r.Integrated, r.Range, r.TruePeak)
	fmt.Println(loudness.DefaultTarget().Check(r).OK())
	// Output:
	// -23.0 LUFS, LRA 0.0 LU, true peak -23.0 dBTP
	// true
}
