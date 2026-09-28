# Contributing

Thanks for your interest in qc! Bug reports, ideas, validation
results on new kinds of content and pull requests are all welcome.

## Development setup

```sh
git clone https://github.com/eko/qc.git
cd qc
```

You need:

- Go (see `go.mod` for the version) with cgo enabled
- ffmpeg and ffprobe built with libx264, libx265 and libsvtav1 (and
  libass for the annotated videos of `--overlay`: their tests skip without
  it)
- libvmaf ≥ 3.2.1 with its models and `pkg-config` (3.2.0 cannot load the VMAF v1 models)
- [golangci-lint](https://golangci-lint.run/) v2

On macOS: `brew install ffmpeg libvmaf pkg-config golangci-lint`.

```sh
make build        # bin/qc
make check        # vet + lint + tests with the race detector (what CI runs)
make cover        # coverage summary; make cover-html to browse it
make docker-test  # build the Docker image and smoke-test it
```

Linux prerequisites (libvmaf from source) are in
[docs/install.md](docs/install.md); `qc version --check` tells what is
missing. Releases are described in [RELEASING.md](RELEASING.md).

Tests that need real media generate tiny clips with ffmpeg's lavfi sources
(see `internal/testutil`) and are skipped when ffmpeg is missing. The whole
suite runs in about a minute.

## Architecture

Services depend on small interfaces (ports) declared by the package that
consumes them, not by the one implementing them: `ladder.Encoder` lives in
`ladder`, `analysis.Meter` in `analysis`. Adapters (`encode.FFmpeg`,
`quality.Meter`, `libvmaf.Engine`...) satisfy them without knowing their
consumers, and libraries take them through their constructors. The CLI is
the only composition root: to wire a new service, add its constructor to
the fx module in `cmd/qc/wire.go`, provided behind the port its consumer
declares with `fx.As`, and give it only the configuration group it needs.
[docs/architecture.md](docs/architecture.md#ports) lists the ports.

golangci-lint guards these boundaries (`depguard` in `.golangci.yml`): only
the composition roots (`cmd/`, `bench/`) import `vmaf/libvmaf`; only the
ffmpeg/ffprobe adapters run subprocesses (`internal/ffexec`, `os/exec`); the
presenters (`internal/tui`, `internal/htmlreport`, `internal/findings`) and
the CLI stack (cobra, viper, fx) stay out of the library. Production
functions stay under 60 lines and interfaces under 6 methods; a deliberate
exception carries a `//nolint` with its reason.

Only `vmaf/libvmaf` uses cgo: `make nocgo` checks that the service packages
still build without it. GPU code is tested with fakes by default; the real
CUDA paths build and run with `-tags cuda` against a CUDA libvmaf (`docker
build -f Dockerfile.cuda --target test .`, see [docs/gpu.md](docs/gpu.md)).

## Guidelines

- **Tests**: new code comes with tests, including error paths. Tests are
  table-driven with testify:

  ```go
  testCases := []struct {
  	name string
  	// inputs and expectations
  }{
  	{name: "…"},
  }

  for _, testCase := range testCases {
  	t.Run(testCase.name, func(t *testing.T) {
  		// …
  	})
  }
  ```

  Prefer fakes of the small interfaces (`probe.Prober`, `decode.Source`,
  `ladder.Encoder`, `ladder.Inspector`…) for logic, and a tiny generated clip
  for the paths that talk to ffmpeg or libvmaf.
- **Style**: `gofmt`, golangci-lint clean (the configuration is in
  `.golangci.yml`). Keep functions small and name things for what they mean.
  Explain *why* in comments, not *what*. Every exported symbol has a doc
  comment. Wrap errors with context (`fmt.Errorf("…: %w", err)`). Constants
  with a meaning get a name and a comment explaining the value.
- **Algorithms**: the VMAF sampler and the ladder engine are validated against
  ground truth. A change to their behaviour must come with the validation it
  passed: real interval coverage with `bench/vmafsim`, and distance to the
  exhaustive optimum with `bench/ladderval`, and the audio meters against
  the EBU conformance signals and ffmpeg with `bench/audioval` (see
  [docs/validation.md](docs/validation.md)). Include the numbers in the pull
  request.
- **Docs**: update `docs/` and the README when behaviour or flags change.

## Reporting issues

Please include the command line, the output of `qc version --check` (the
versions of qc, ffmpeg and libvmaf, and what is missing), and if
possible a short clip, or the ffmpeg command that generates one, that shows the
problem.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
