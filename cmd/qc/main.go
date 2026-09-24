// Command qc analyses video files: technical metrics, VMAF against a
// reference and per-title streaming ladders (H.264, HEVC, AV1).
//
// Run qc without arguments in a terminal for an interactive wizard, or see
// qc --help for the commands.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, detectEnvironment())

	stop()
	os.Exit(code)
}

// run executes qc with args and returns the process exit code.
func run(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	env environment,
) int {
	// Cobra builds commands without a context and hands ctx to them through
	// ExecuteContext (cmd.Context()), which contextcheck cannot follow.
	root := newRootCommand(env) //nolint:contextcheck // ctx reaches the commands through ExecuteContext
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, "qc:", err)

		return 1
	}

	return 0
}
