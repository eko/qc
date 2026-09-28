# syntax=docker/dockerfile:1
#
# qc, CPU image: qc, libvmaf 3.2.1 with the VMAF v1 models, and ffmpeg and
# ffprobe with libx264, libx265, libsvtav1, libdav1d and libass (the
# annotated videos of --overlay, drawn in DejaVu Sans Mono).
#
#   docker build -t qc .
#   docker run --rm -v "$PWD:/data" qc vmaf reference.mov encode.mp4
#
# Multi-arch: docker buildx build --platform linux/amd64,linux/arm64 .
#
# ffmpeg is built here rather than installed from Debian: Debian's package
# pulls Mesa, LLVM, Vulkan and audio stacks qc never uses (a 750 MB image
# instead of ~200 MB), and ships SVT-AV1 2.3 while Homebrew, the reference
# build of CI, has SVT-AV1 4.x. x264, x265 and dav1d come from Debian (and
# its security updates).

ARG GO_VERSION=1.27.1
ARG DEBIAN_RELEASE=trixie

# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION}-${DEBIAN_RELEASE} AS toolchain

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        cmake meson nasm ninja-build xxd xz-utils \
        libass-dev libdav1d-dev libx264-dev libx265-dev zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# libvmaf from source: Debian's is older, and 3.2.0 cannot load the VMAF v1
# models qc uses by default. Same options as Homebrew's formula, minus what a
# runtime image does not need.
FROM toolchain AS libvmaf

ARG LIBVMAF_VERSION=3.2.1
ARG LIBVMAF_SHA256=5df7386911bc15fd1ca783132528748d219768ae4fc5f8e0b61184f041648092

WORKDIR /src/vmaf
RUN curl -fsSL -o vmaf.tar.gz "https://github.com/Netflix/vmaf/archive/refs/tags/v${LIBVMAF_VERSION}.tar.gz" \
    && echo "${LIBVMAF_SHA256}  vmaf.tar.gz" | sha256sum -c - \
    && tar -xzf vmaf.tar.gz --strip-components=1 \
    && rm vmaf.tar.gz \
    && meson setup build libvmaf \
        --prefix=/usr/local --libdir=lib --buildtype=release \
        -Denable_tests=false -Denable_docs=false -Denable_tools=false \
    && meson compile -C build \
    && meson install -C build \
    && mkdir -p /usr/local/share/libvmaf \
    && cp -r model /usr/local/share/libvmaf/model \
    && ldconfig \
    && mkdir -p /runtime/usr/local/lib /runtime/usr/local/share/libvmaf \
    && cp -a /usr/local/lib/libvmaf.so.* /runtime/usr/local/lib/ \
    && cp -a /usr/local/share/libvmaf/model /runtime/usr/local/share/libvmaf/

# ---------------------------------------------------------------------------
# SVT-AV1, pinned by tag and commit (GitLab archives are not byte-stable).
FROM toolchain AS svtav1

ARG SVTAV1_VERSION=4.2.0
ARG SVTAV1_COMMIT=9292ec8e32bce26f781f277ec8739b53426c4300

WORKDIR /src/svtav1
RUN git clone --depth 1 --branch "v${SVTAV1_VERSION}" https://gitlab.com/AOMediaCodec/SVT-AV1.git . \
    && test "$(git rev-parse HEAD)" = "${SVTAV1_COMMIT}" \
    && cmake -S . -B build -G Ninja \
        -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr/local -DCMAKE_INSTALL_LIBDIR=lib \
        -DBUILD_APPS=OFF -DBUILD_TESTING=OFF -DBUILD_SHARED_LIBS=ON \
    && cmake --build build \
    && cmake --install build \
    && ldconfig

# ---------------------------------------------------------------------------
# ffmpeg with only what qc needs: every native decoder, demuxer and filter,
# the three ladder encoders and dav1d to decode AV1 rungs, libass for the
# subtitles filter that burns the --overlay, plus the lavfi device to
# generate test clips. No autodetected dependency (X11, Vulkan, VAAPI,
# SDL…), no network, no ffplay.
FROM svtav1 AS ffmpeg

ARG FFMPEG_VERSION=9.0.2
ARG FFMPEG_SHA256=8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e

WORKDIR /src/ffmpeg
RUN curl -fsSL -o ffmpeg.tar.xz "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" \
    && echo "${FFMPEG_SHA256}  ffmpeg.tar.xz" | sha256sum -c - \
    && tar -xJf ffmpeg.tar.xz --strip-components=1 \
    && rm ffmpeg.tar.xz \
    && ./configure \
        --prefix=/usr/local \
        --enable-gpl \
        --enable-shared --disable-static \
        --disable-autodetect --enable-zlib \
        --enable-libass --enable-libdav1d --enable-libsvtav1 --enable-libx264 --enable-libx265 \
        --disable-debug --disable-doc --disable-ffplay --disable-network \
    && make -j"$(nproc)" \
    && make install \
    && ldconfig \
    && ffmpeg -hide_banner -filters | grep -q " subtitles " \
    && mkdir -p /runtime/usr/local/lib /runtime/usr/local/bin \
    && cp -a /usr/local/lib/libSvtAv1Enc.so.* /usr/local/lib/libav*.so.* /usr/local/lib/libsw*.so.* /runtime/usr/local/lib/ \
    && cp /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /runtime/usr/local/bin/

# ---------------------------------------------------------------------------
# qc, linked against the libvmaf above.
FROM libvmaf AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

WORKDIR /src/qc
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
        -o /out/qc ./cmd/qc

# ---------------------------------------------------------------------------
FROM debian:${DEBIAN_RELEASE}-slim

ARG VERSION=dev

LABEL org.opencontainers.image.title="qc" \
      org.opencontainers.image.description="Fast video analysis: technical metrics, VMAF and per-title streaming ladders" \
      org.opencontainers.image.source="https://github.com/eko/qc" \
      org.opencontainers.image.url="https://github.com/eko/qc" \
      org.opencontainers.image.documentation="https://github.com/eko/qc/blob/main/docs/install.md" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

# libass draws the --overlay in DejaVu Sans Mono (overlay.DefaultFont).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        fonts-dejavu-mono libass9 libdav1d7 libx264-164 libx265-215 \
    && rm -rf /var/lib/apt/lists/*

# The runtime trees keep the library symlinks (COPY of a glob would copy
# every library twice).
COPY --from=ffmpeg /runtime/ /
# /usr/local/share/libvmaf/model is one of vmaf.DefaultModelDirs: qc finds
# the models without --model-dir.
COPY --from=libvmaf /runtime/ /
COPY --from=build /out/qc /usr/local/bin/qc

RUN ldconfig \
    && useradd --uid 10001 --user-group --no-create-home --home-dir /data qc \
    && mkdir -p /data \
    && chown qc:qc /data \
    && ffmpeg -hide_banner -version > /dev/null \
    && qc version --check > /dev/null

USER qc
WORKDIR /data

ENTRYPOINT ["qc"]
CMD ["--help"]
