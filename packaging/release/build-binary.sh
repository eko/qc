#!/bin/sh
# Builds a self-contained qc binary for the release: libvmaf compiled as a
# static library with its built-in models (the VMAF v1 ones included), qc
# linked against it, checked, and packed into
#
#   <out>/qc_<version>_<os>_<arch>.tar.gz
#
# On Linux (run it in golang:<go>-alpine) the binary is fully static (musl):
# it runs on any distribution. On macOS only the system libraries stay
# dynamic. Either way it needs no libvmaf and no model files, only ffmpeg
# and ffprobe at run time.
#
#   packaging/release/build-binary.sh <version> <commit> <date> <out>
#
# Linux needs: apk add build-base curl meson nasm ninja pkgconf xxd
# macOS needs: brew install meson ninja pkgconf (xxd ships with macOS)
set -eu

version=$1
commit=$2
date=$3
out=$4

LIBVMAF_VERSION=3.2.1
LIBVMAF_SHA256=5df7386911bc15fd1ca783132528748d219768ae4fc5f8e0b61184f041648092

os=$(go env GOOS)
arch=$(go env GOARCH)
root=$(pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

# libvmaf, static, with its models compiled in (built_in_models). The
# prefix holds only the static library, so -lvmaf cannot pick a shared one.
curl -fsSL -o "$work/vmaf.tar.gz" "https://github.com/Netflix/vmaf/archive/refs/tags/v${LIBVMAF_VERSION}.tar.gz"
echo "${LIBVMAF_SHA256}  $work/vmaf.tar.gz" | sha256 -c -
mkdir "$work/vmaf"
tar -xzf "$work/vmaf.tar.gz" -C "$work/vmaf" --strip-components=1
meson setup "$work/build" "$work/vmaf/libvmaf" \
	--prefix="$work/prefix" --libdir=lib --buildtype=release \
	--default-library=static -Dbuilt_in_models=true \
	-Denable_tests=false -Denable_docs=false -Denable_tools=false
meson compile -C "$work/build"
meson install -C "$work/build"

export PKG_CONFIG_PATH="$work/prefix/lib/pkgconfig"
export CGO_ENABLED=1
# The static library's own dependencies, which cgo's pkg-config call leaves
# out: libm and pthread, and the C++ runtime of its SVM code, which
# libvmaf.pc does not declare.
cxx=-lstdc++
if [ "$os" = darwin ]; then
	cxx=-lc++
fi
CGO_LDFLAGS="$(pkg-config --static --libs libvmaf) $cxx"
export CGO_LDFLAGS

ldflags="-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${date}"
if [ "$os" = linux ]; then
	ldflags="$ldflags -linkmode external -extldflags -static"
fi

bin="$work/qc"
# The commit comes from the ldflags: no git needed in the build container.
go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$bin" ./cmd/qc

# The binary must not need anything the release does not ship.
case "$os" in
linux)
	if file "$bin" | grep -q dynamically; then
		file "$bin"
		echo "qc is not static" >&2
		exit 1
	fi
	;;
darwin)
	if otool -L "$bin" | tail -n +2 | grep -vE '^\s+/(usr/lib|System/Library)/'; then
		echo "qc links libraries outside the system" >&2
		exit 1
	fi
	;;
esac

"$bin" version

name="qc_${version#v}_${os}_${arch}"
mkdir -p "$work/$name" "$out"
cp "$bin" "$work/$name/qc"
cp "$root/LICENSE" "$root/README.md" "$work/$name/"
# libvmaf is linked in: its licence travels with it.
cp "$work/vmaf/LICENSE" "$work/$name/LICENSE.libvmaf"
tar -czf "$out/$name.tar.gz" -C "$work" "$name"
echo "$out/$name.tar.gz"
