package main

import (
	"context"

	"github.com/eko/qc/internal/ffexec"
)

// runner runs a command and returns its standard output; a failure carries
// the beginning of its standard error. Tests replace it with a fake.
type runner interface {
	run(
		ctx context.Context,
		name string,
		args ...string,
	) ([]byte, error)
}

// execRunner runs real processes.
type execRunner struct{}

func (execRunner) run(
	ctx context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	return ffexec.Output(ctx, name, args)
}
