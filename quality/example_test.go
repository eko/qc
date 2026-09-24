package quality_test

import (
	"fmt"

	"github.com/eko/qc/quality"
)

// A fixed budget replaces the precision target: a share of the frames, or a
// number of clips in every scene. The CLI's --sample flag takes the same
// syntax.
func ExampleParseSample() {
	for _, s := range []string{"5%", "2/scene"} {
		budget, err := quality.ParseSample(s)
		if err != nil {
			fmt.Println(err)

			continue
		}

		fmt.Printf("%s: share %.2f, %d per scene\n", budget, budget.Share, budget.PerScene)
	}

	_, err := quality.ParseSample("0%")
	fmt.Println(err != nil)
	// Output:
	// 5%: share 0.05, 0 per scene
	// 2/scene: share 0.00, 2 per scene
	// true
}

// Metrics other than VMAF and the VMAF of viewing devices are chosen by
// name; ParseMetrics validates them early.
func ExampleParseMetrics() {
	metrics, err := quality.ParseMetrics([]string{"XPSNR", "cambi"})
	fmt.Println(metrics, err)

	_, err = quality.ParseMetrics([]string{"ssimulacra2"})
	fmt.Println(err)
	// Output:
	// [xpsnr cambi] <nil>
	// unknown metric "ssimulacra2" (supported: vmaf, xpsnr, cambi, psnr, psnr-hvs, ssim, ms-ssim, ciede2000)
}
