package ladder

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/encode"
)

// CalibrationTolerance is the VMAF gap between prediction and verification
// beyond which a rung's CRF is corrected by one secant step (the rung is
// then Calibrated), TopCalibrationTolerance for the top rung. Reports quote
// it when they flag calibrated rungs.
const CalibrationTolerance = 1.5

// verifyAndCalibrate measures every rung with its final settings, then
// corrects the CRF of rungs whose quality misses the prediction (rate
// control, VBV or curve error) and measures them again.
func (b *build) verifyAndCalibrate(
	ctx context.Context,
	rungs []Rung,
	probes []Probe,
) error {
	if err := b.verify(ctx, rungs, func(int) bool { return true }); err != nil {
		return err
	}

	missed := func(i int) bool {
		return math.Abs(rungs[i].Measured.VMAF-rungs[i].PredictedVMAF) > calibrationTolerance(i)
	}

	first, firstCRF := *rungs[0].Measured, rungs[0].CRF

	if !b.calibrate(rungs, probes, missed) {
		return nil
	}

	if err := b.verify(ctx, rungs, func(i int) bool { return rungs[i].Calibrated }); err != nil {
		return err
	}

	if err := b.refineTop(ctx, rungs, firstCRF, first, missed); err != nil {
		return err
	}

	// The measured bitrate of a corrected rung is the better estimate of
	// what it will cost (the VBV cap stays the verified one).
	for i := range rungs {
		if r := &rungs[i]; r.Calibrated {
			r.Bitrate = r.Measured.Bitrate
		}
	}

	return nil
}

// verify encodes the digest with the final settings of the selected rungs,
// VBV included, and measures them.
func (b *build) verify(
	ctx context.Context,
	rungs []Rung,
	selected func(int) bool,
) error {
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(b.opts.Parallel)
	b.resetProgress()

	total := 0
	for i := range rungs {
		if selected(i) {
			total++
		}
	}

	for i := range rungs {
		if !selected(i) {
			continue
		}

		group.Go(func() error {
			r := &rungs[i]
			probe := Probe{Width: r.Width, Height: r.Height, CRF: r.CRF}
			rate := encode.Params{MaxRate: r.MaxRate, BufSize: r.BufSize}

			var after func(string) error
			if b.grain > 0 {
				after = func(path string) error {
					check, err := b.grainCheck(gctx, path, *r)
					r.Grain = check

					return err
				}
			}

			// The top rung is scored on every frame: it is the quality the
			// ladder promises.
			mode := scoreRung
			if i == 0 {
				mode = scoreRungExact
			}

			m, err := b.measure(gctx, probe, rate, fmt.Sprintf("verify-%d", i), mode, after)
			if err != nil {
				return err
			}

			r.Measured = &m
			rung := *r
			b.tick(Progress{Stage: StageVerify, Total: total, Rung: &rung})

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return fmt.Errorf("ladder: verify: %w", err)
	}

	return nil
}

// calibrate corrects the CRF of the selected measured rungs by one secant
// step on the quality/CRF slope of their resolution's probes, and reports
// whether any rung changed. Commands are rebuilt accordingly.
func (b *build) calibrate(
	rungs []Rung,
	probes []Probe,
	selected func(int) bool,
) bool {
	changed := false

	for i := range rungs {
		r := &rungs[i]
		if r.Measured == nil || !selected(i) {
			continue
		}

		// Quality must fall as CRF grows; noisy probes can break that.
		slope := crfSlope(probes, r.Height, r.CRF)
		if slope >= 0 {
			continue
		}

		crf := b.roundCRF(r.CRF + (r.PredictedVMAF-r.Measured.VMAF)/slope)
		if crf == r.CRF {
			continue
		}

		r.CRF, r.Calibrated, changed = crf, true, true
		r.Command = b.command(i, *r)
	}

	return changed
}

// crfSlope is dVMAF/dCRF between the two probes of a resolution surrounding
// crf (the nearest two at either end), normally negative. It is 0 when the
// resolution has fewer than two probes of distinct CRFs.
func crfSlope(
	probes []Probe,
	height int,
	crf float64,
) float64 {
	var ps []Probe

	for _, p := range probes {
		if p.Height == height {
			ps = append(ps, p)
		}
	}

	if len(ps) < 2 {
		return 0
	}

	slices.SortFunc(ps, func(a, c Probe) int { return cmp.Compare(a.CRF, c.CRF) })

	i := 1
	for i < len(ps)-1 && ps[i].CRF < crf {
		i++
	}

	lo, hi := ps[i-1], ps[i]
	if hi.CRF == lo.CRF {
		return 0
	}

	return (hi.VMAF - lo.VMAF) / (hi.CRF - lo.CRF)
}

// refineTop corrects the top rung once more when it still misses its
// prediction after calibration, by a secant step between its two
// measurements (both on every frame of the digest), which know its slope
// better than the probes around it.
func (b *build) refineTop(
	ctx context.Context,
	rungs []Rung,
	prevCRF float64,
	prev Measurement,
	missed func(int) bool,
) error {
	top := &rungs[0]
	if !top.Calibrated || !missed(0) {
		return nil
	}

	crf, action := topStep(prevCRF, prev.VMAF, top.CRF, top.Measured.VMAF, top.PredictedVMAF, b.roundCRF)

	switch action {
	case stepKeep:
		return nil
	case stepRestore:
		top.CRF, top.Measured = prevCRF, &prev
		top.Command = b.command(0, *top)

		return nil
	}

	top.CRF = crf
	top.Command = b.command(0, *top)

	return b.verify(ctx, rungs, func(i int) bool { return i == 0 })
}

// stepAction is what the second step of the top rung does.
type stepAction int

const (
	// stepKeep keeps the current CRF.
	stepKeep stepAction = iota
	// stepRestore goes back to the first CRF, already measured.
	stepRestore
	// stepEncode encodes and measures a new CRF.
	stepEncode
)

// topStep is the secant step from the top rung's first measurement
// (prevCRF, prevVMAF) and its second (crf, vmaf) towards target, rounded
// by round: keep the current CRF when the measurements do not fall with
// CRF or the step stays put, restore the first when the step returns to it.
func topStep(
	prevCRF, prevVMAF, crf, vmaf, target float64,
	round func(float64) float64,
) (float64, stepAction) {
	if crf == prevCRF {
		return crf, stepKeep
	}

	slope := (vmaf - prevVMAF) / (crf - prevCRF)
	if slope >= 0 {
		return crf, stepKeep
	}

	next := round(crf + (target-vmaf)/slope)

	switch next {
	case crf:
		return crf, stepKeep
	case prevCRF:
		// The target lies nearer the first measurement, already made.
		return prevCRF, stepRestore
	}

	return next, stepEncode
}
