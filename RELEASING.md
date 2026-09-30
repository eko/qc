# Releasing

Releases are cut from `main` by pushing a `vX.Y.Z` tag. The
[release workflow](.github/workflows/release.yml) then:

1. runs `make check` (vet, golangci-lint, tests with the race detector) on
   macOS, like CI;
2. builds the Docker image on a native runner per architecture
   (linux/amd64, linux/arm64), with provenance and an SBOM, smoke-tests it
   (`packaging/docker/smoke-test.sh`) and pushes it by digest;
3. tags the multi-arch image `ghcr.io/eko/qc:X.Y.Z`, `X.Y` and `latest` (a
   pre-release such as `v0.2.0-rc.1` only gets its own tag);
4. builds `Dockerfile.cuda`, when the repository has one, as
   `ghcr.io/eko/qc:X.Y.Z-cuda` (linux/amd64);
5. builds the self-contained binaries on native runners
   (`packaging/release/build-binary.sh`: libvmaf static with its built-in
   models; Linux amd64/arm64 fully static with musl, macOS arm64) and
   smoke-tests each one with ffmpeg only (`packaging/release/smoke-binary.sh`);
6. creates the GitHub release, with the `## [X.Y.Z]` section of
   [CHANGELOG.md](CHANGELOG.md) as its notes, the binaries and their
   `SHA256SUMS`. The workflow fails early when that section is missing.

## Steps

1. **Changelog**: rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, add
   a new empty `## [Unreleased]` above it, and update the links at the
   bottom:

   ```markdown
   [Unreleased]: https://github.com/eko/qc/compare/vX.Y.Z...HEAD
   [X.Y.Z]: https://github.com/eko/qc/releases/tag/vX.Y.Z
   ```

   (from the second release on, `[X.Y.Z]` compares with the previous tag).
2. **Check** locally: `make check` and `make docker-test`. Commit
   ("Release vX.Y.Z") and push to `main`; wait for CI to be green.
3. **Tag and push**:

   ```sh
   git tag -a vX.Y.Z -m "qc vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. **Watch** the workflow (`gh run watch`), then check the release page and
   the image:

   ```sh
   docker run --rm ghcr.io/eko/qc:X.Y.Z version --check
   ```

   The first time, make the `qc` package public in the GitHub package
   settings (ghcr.io packages are private by default) and link it to the
   repository.
5. **Homebrew**: update the formula of the
   [eko/homebrew-tap](https://github.com/eko/homebrew-tap) repository from
   [packaging/homebrew/qc.rb](packaging/homebrew/qc.rb):

   ```sh
   VERSION=X.Y.Z
   curl -fsSL "https://github.com/eko/qc/archive/refs/tags/v$VERSION.tar.gz" | shasum -a 256
   ```

   Set `url` to that tarball and `sha256` to the printed checksum in
   `packaging/homebrew/qc.rb` (commit it here too), copy it to
   `Formula/qc.rb` in the tap, then check it:

   ```sh
   brew install --build-from-source eko/tap/qc
   brew test eko/tap/qc
   brew audit --strict --online eko/tap/qc
   ```

6. **Go module proxy**: `go install github.com/eko/qc/cmd/qc@vX.Y.Z` works as
   soon as the tag is public; `GOPROXY=https://proxy.golang.org go list -m
   github.com/eko/qc@vX.Y.Z` makes pkg.go.dev pick it up.

## Versioning

qc follows [Semantic Versioning](https://semver.org/). Until 1.0.0, minor
versions may change the CLI flags, the JSON report schema (see its
`schemaVersion`) and the library API; patch versions only fix bugs.

## Version information

Release builds stamp `main.version`, `main.commit` and `main.date` with
`-ldflags -X` (the Dockerfile, the Makefile and the Homebrew formula do).
`go install …@vX.Y.Z` builds fall back to the module version recorded by Go.
`qc version` prints them with the Go and libvmaf versions; `qc version
--check` also checks ffmpeg, the encoders and the VMAF models.
