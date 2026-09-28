package findings

// Verdict is the overall outcome of a report, read from its findings: what
// a reviewer needs to know before looking at any number.
type Verdict int

// Verdicts, best first.
const (
	// Pass: no warning.
	Pass Verdict = iota
	// Attention: warnings worth a look, none of them blocking.
	Attention
	// Fail: at least one blocking finding.
	Fail
)

// String is the lower-case name of the verdict: pass, attention or fail.
func (v Verdict) String() string {
	switch v {
	case Attention:
		return "attention"
	case Fail:
		return "fail"
	}

	return "pass"
}

// blocking are the warnings that fail a report: a deliverable carrying one
// breaks its specification (the true-peak ceiling, the HDR10 signal) or is
// broken for every viewer (a silent track, a muted or inverted channel, no
// ladder at all). Other warnings depend on intent — a fade to black, a still
// frame, a peaky bitrate — and only ask for a look. So does integrated
// loudness off its target: the default EBU R 128 target does not fit every
// delivery (web and streaming mixes run louder), so it asks for a check
// against the right target rather than failing the report.
var blocking = map[Code]bool{
	SilentTrack:      true,
	MutedChannel:     true,
	InvertedPolarity: true,
	TruePeakOver:     true,
	HDRPrimaries:     true,
	HDRMatrix:        true,
	HDRBitDepth:      true,
	NoRungs:          true,
}

// Blocking reports whether the finding fails the report on its own.
func (f Finding) Blocking() bool {
	return f.Level == Warn && blocking[f.Code]
}

// Judge is the verdict of a report's findings: Fail on a blocking finding,
// Attention on any other warning, Pass otherwise. Notes and passed checks
// never lower the verdict.
func Judge(
	list []Finding,
) Verdict {
	verdict := Pass

	for _, f := range list {
		switch {
		case f.Blocking():
			return Fail
		case f.Level == Warn:
			verdict = Attention
		}
	}

	return verdict
}
