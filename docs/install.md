# Install

qc needs three things at run time:

- **ffmpeg and ffprobe** with the libx264, libx265 and libsvtav1 encoders
  (ladders) and a software AV1 decoder (libdav1d);
- **libvmaf ≥ 3.2.1**, which qc is linked against (cgo). 3.2.0 cannot load
  the VMAF v1 models qc uses by default;
- the **VMAF v1.0.16 models** (`vmaf_v1.0.16_3d0h.json`…), looked up in
  `/opt/homebrew/share/libvmaf/model`, `/usr/local/share/libvmaf/model` and
  `/usr/share/libvmaf/model` (or `--model-dir`, `QC_MODEL_DIR`), then among
  the models built into libvmaf.

Optionally, ffmpeg built with **libass** (its `subtitles` filter) and a
monospaced font (Menlo on macOS, DejaVu Sans Mono elsewhere) for the
annotated videos of `--overlay` ([overlay.md](overlay.md)).

The Docker image and the Homebrew formula bring all of them; the prebuilt
binaries bring libvmaf and the models, and need ffmpeg only. Whatever the
method, check the result with:

```sh
qc version --check
```

```
qc       v0.1.0
commit   0123456789ab
built    2026-09-25T10:00:00Z
go       go1.27.1 linux/arm64
libvmaf  3.2.0

✓ ffmpeg      9.0.2
✓ ffprobe     9.0.2
✓ libx264
✓ libx265
✓ libsvtav1
- nvenc       not available (optional)
✓ libass      subtitles filter (--overlay)
✓ vmaf model  /usr/local/share/libvmaf/model/vmaf_v1.0.16/vmaf_v1.0.16_3d0h.json
```

It exits with an error when a requirement is missing, and `-f json` prints
the same as JSON: paste either in bug reports.

## Docker

The image `ghcr.io/eko/qc` (linux/amd64 and linux/arm64) contains qc,
libvmaf 3.2.1 with its models and ffmpeg. Mount the directory holding your
videos on `/data`, the working directory of the image:

```sh
docker pull ghcr.io/eko/qc:latest

docker run --rm -v "$PWD:/data" ghcr.io/eko/qc analyze video.mp4
docker run --rm -v "$PWD:/data" ghcr.io/eko/qc vmaf reference.mov encode.mp4 --sample 5%
docker run --rm -v "$PWD:/data" ghcr.io/eko/qc ladder source.mov -c av1 --html ladder.html
docker run --rm -v "$PWD:/data" ghcr.io/eko/qc run source.mov --codecs h264,av1 -o report.json --html report.html
```

Paths are relative to `/data`. Reports written with `-o` and `--html` land in
the mounted directory.

- **Linux hosts**: the image runs as an unprivileged user (uid 10001). To
  write reports into a directory you own, run as yourself:
  `docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/data" ghcr.io/eko/qc …`.
- **Read-only sources**: mount them separately, e.g.
  `-v /mnt/masters:/masters:ro -v "$PWD:/data"`, then
  `qc vmaf /masters/source.mov encode.mp4 -o report.json`.
- **Live dashboard**: add `-it` to get the dashboard and the wizard
  (`docker run --rm -it -v "$PWD:/data" ghcr.io/eko/qc`). Without a terminal,
  output stays plain, as in CI.
- **Configuration**: every flag has a `QC_` environment variable:
  `-e QC_PRECISION=0.25 -e QC_LOG_LEVEL=info`.
- **An alias** makes the image feel native:

  ```sh
  alias qc='docker run --rm -it --user "$(id -u):$(id -g)" -v "$PWD:/data" ghcr.io/eko/qc'
  qc vmaf reference.mov encode.mp4
  ```

- **Test clips**: the image's ffmpeg has lavfi, so you can try qc without
  media of your own:

  ```sh
  docker run --rm -v "$PWD:/data" --entrypoint ffmpeg ghcr.io/eko/qc \
    -f lavfi -i testsrc2=size=1280x720:rate=25:duration=10 -c:v libx264 -crf 12 source.mp4
  docker run --rm -v "$PWD:/data" ghcr.io/eko/qc ladder source.mp4 -c h264
  ```

Tags: `latest`, the release (`0.1.0`) and its minor series (`0.1`). An NVIDIA
variant, `<version>-cuda` (linux/amd64), is published when the release
includes it; see [gpu.md](gpu.md).

On macOS, Docker runs in a virtual machine: scores match a native build
(VMAF means within 1e-6 of a native macOS build on the same input, identical
ladders) but a native install is faster on long titles, and the VM only sees the CPUs and memory given to Docker Desktop.

### What is inside

| Component | Version | Source |
|---|---|---|
| qc | the release | built with cgo, `-trimpath` |
| libvmaf | 3.2.1 + v1.0.16 models | built from the release tarball, checksum pinned |
| ffmpeg, ffprobe | 9.0.2 | built from the release tarball, checksum pinned: every native decoder, demuxer and filter, lavfi, libass (`--overlay`); no network, no hardware acceleration |
| SVT-AV1 | 4.2.0 | built from the tag, commit pinned (Debian ships 2.3) |
| x264, x265, dav1d, libass, DejaVu Sans Mono | Debian trixie | Debian packages and their security updates |

Base: `debian:trixie-slim`, about 250 MB unpacked (63 MB compressed;
libass, its dependencies and the font take 16 MB of it). ffmpeg is built
with `--enable-gpl` (x264, x265), so the image as a whole is distributed
under the GPL; the sources are the pinned upstream releases listed in the
[Dockerfile](../Dockerfile).

### Building the image

```sh
make docker        # docker build -t qc . with the version of the checkout
make docker-test   # smoke tests on synthetic clips: version --check, analyze, vmaf, h264 and av1 ladders
docker buildx build --platform linux/amd64,linux/arm64 -t qc .   # both architectures
```

Building for the other architecture runs under QEMU and takes much longer
(ffmpeg, SVT-AV1 and libvmaf are compiled): the release workflow builds each
architecture on a native runner instead.

## Prebuilt binaries

Every [release](https://github.com/eko/qc/releases) has self-contained
binaries: libvmaf 3.2.1 is linked in, with the VMAF models built into it,
so they need nothing but ffmpeg and ffprobe (with libx264, libx265 and
libsvtav1) at run time.

| Archive | Platform | Linking |
|---|---|---|
| `qc_<version>_linux_amd64.tar.gz` | Linux x86-64 | static (musl): any distribution |
| `qc_<version>_linux_arm64.tar.gz` | Linux arm64 (Graviton, Ampere, Raspberry Pi 4/5 64-bit) | static (musl) |
| `qc_<version>_darwin_arm64.tar.gz` | macOS on Apple silicon | system libraries only |

```sh
VERSION=1.0.0
OS=linux ARCH=amd64    # or linux/arm64, darwin/arm64
curl -fsSLO "https://github.com/eko/qc/releases/download/v$VERSION/qc_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/eko/qc/releases/download/v$VERSION/SHA256SUMS"
sha256sum --ignore-missing -c SHA256SUMS   # macOS: shasum -a 256 --ignore-missing -c SHA256SUMS
tar -xzf "qc_${VERSION}_${OS}_${ARCH}.tar.gz"
sudo install "qc_${VERSION}_${OS}_${ARCH}/qc" /usr/local/bin/
qc version --check
```

Each archive also holds the licences of qc and of libvmaf (BSD-2-Clause
Patent), which the binary embeds.

**macOS: the binary is not signed.** A binary downloaded by a browser is
quarantined, and Gatekeeper refuses to open it ("cannot be opened because
the developer cannot be verified"). Downloaded with `curl` as above, it is
not quarantined. Otherwise, lift the quarantine once:

```sh
xattr -d com.apple.quarantine /usr/local/bin/qc
```

Install ffmpeg with `brew install ffmpeg` (macOS) or your distribution's
package (Debian 13 and Ubuntu 24.04 ship the three encoders).

## Homebrew (macOS, Linux)

```sh
brew install eko/tap/qc
qc version --check
```

The formula builds qc from the release sources against Homebrew's
`libvmaf` (3.2.1, with the v1.0.16 models in `share/libvmaf/model`) and
`ffmpeg` (with x264, x265 and SVT-AV1). On Linux, the installed `qc` sets
`QC_MODEL_DIR` to Linuxbrew's model directory, unless you set it yourself.

## From source (`go install`)

You need Go (see `go.mod`) with cgo and a C compiler, pkg-config, ffmpeg with
the three encoders, and libvmaf ≥ 3.2.1 with its models.

### macOS

```sh
xcode-select --install                # C compiler, if missing
brew install go ffmpeg libvmaf pkgconf
go install github.com/eko/qc/cmd/qc@latest
qc version --check
```

### Debian 13 / Ubuntu 24.04

The distributions' ffmpeg has libx264, libx265 and libsvtav1, but they do
not ship libvmaf ≥ 3.2.1: build it from source (a minute), with its models.

```sh
sudo apt-get install -y build-essential pkg-config curl ffmpeg meson ninja-build nasm xxd

curl -fsSL -o vmaf-3.2.1.tar.gz https://github.com/Netflix/vmaf/archive/refs/tags/v3.2.1.tar.gz
echo "5df7386911bc15fd1ca783132528748d219768ae4fc5f8e0b61184f041648092  vmaf-3.2.1.tar.gz" | sha256sum -c -
tar -xzf vmaf-3.2.1.tar.gz && cd vmaf-3.2.1
meson setup build libvmaf --prefix=/usr/local --libdir=lib --buildtype=release \
  -Denable_tests=false -Denable_docs=false
sudo meson install -C build
sudo mkdir -p /usr/local/share/libvmaf && sudo cp -r model /usr/local/share/libvmaf/
sudo ldconfig
cd ..
```

Then install Go (from [go.dev/dl](https://go.dev/dl/): the distributions'
packages are usually older than `go.mod` requires) and qc:

```sh
CGO_ENABLED=1 go install github.com/eko/qc/cmd/qc@latest
qc version --check
```

`go install …@v0.1.0` records the version: `qc version` prints it. A build
from a checkout (`make build`, `make install`) stamps the tag, commit and
build date.

Ubuntu 24.04's ffmpeg is 6.1 with SVT-AV1 1.7: fine for H.264 and HEVC,
while AV1 ladders differ from those of a recent SVT-AV1 (the Docker image and
Homebrew have 4.x).

## Troubleshooting

**`vmaf: model not found`** (or `✗ vmaf model` in `qc version --check`): the
v1.0.16 JSON models are not in any of the model directories. Copy the
`model` directory of the libvmaf sources to `/usr/local/share/libvmaf/model`,
or point qc at yours: `--model-dir /path/to/model` or
`QC_MODEL_DIR=/path/to/model`. The directories are searched recursively.

**`load …vmaf_v1.0.16_3d0h.json (libvmaf ≥ 3.2.1 is required)`**: the models
are found but the linked libvmaf cannot read them: it is 3.2.0 or older.
Upgrade libvmaf and rebuild qc (`go install` again): qc is linked against
the libvmaf present at build time.

**`qc version` says libvmaf 3.2.0 with 3.2.1 installed**: expected. The
3.2.1 release did not bump the version compiled into the library. What
matters is that `qc version --check` loads the VMAF v1 model.

**`Package libvmaf was not found in the pkg-config search path`**: install
pkg-config and libvmaf's development files; for a libvmaf under
`/usr/local`, `export PKG_CONFIG_PATH=/usr/local/lib/pkgconfig`.

**`vmaf/models.go: undefined: Model`** (or `undefined: LoadModel`): cgo is
disabled (`CGO_ENABLED=0`, or cross-compiling), so the libvmaf binding is
left out. Set `CGO_ENABLED=1` and install a C compiler.

**`error while loading shared libraries: libvmaf.so.3`**: libvmaf was
installed into a directory the loader does not search. Run `sudo ldconfig`
(Linux), or set `LD_LIBRARY_PATH=/usr/local/lib`.

**`Unknown encoder 'libsvtav1'`** (or libx264, libx265; `✗ libsvtav1` in
`qc version --check`): your ffmpeg was built without that encoder. Use the
Docker image, Homebrew's ffmpeg, or point qc at another build with
`--ffmpeg /path/to/ffmpeg --ffprobe /path/to/ffprobe`.

**`--overlay: ffmpeg has no subtitles filter`** (`- libass` in `qc version
--check`): your ffmpeg was built without libass. Use the Docker image,
Homebrew's ffmpeg, or a build configured with `--enable-libass`.

**Docker: `permission denied` writing a report**: on Linux the image user
cannot write into your directory. Add `--user "$(id -u):$(id -g)"`.
