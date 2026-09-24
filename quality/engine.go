package quality

import "github.com/eko/qc/vmaf"

// Engine is the VMAF implementation a Meter scores with (libvmaf.Engine,
// which needs cgo). Keeping it behind this port lets the measurement logic
// build and be tested without libvmaf.
type Engine interface {
	// ResolveBackend picks where the features of specs are extracted (see
	// vmaf.ResolveBackend).
	ResolveBackend(
		requested vmaf.Backend,
		specs []vmaf.ModelSpec,
	) (vmaf.BackendChoice, error)
	// LoadModels loads specs once per worker: each clip the worker scores
	// then only creates a scorer.
	LoadModels(
		specs []vmaf.ModelSpec,
	) (vmaf.Models, error)
}
