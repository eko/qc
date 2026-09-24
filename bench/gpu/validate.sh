#!/bin/sh
# GPU validation kit: runs gpuval (bench/gpuval) in the qc CUDA image on an
# NVIDIA machine and leaves gpu-report.md in the output directory, to paste
# back. Needs Docker and the NVIDIA container toolkit (docker run --gpus).
#
#   bench/gpu/validate.sh                       # builds Dockerfile.cuda from this checkout
#   QC_IMAGE=ghcr.io/eko/qc:<version>-cuda bench/gpu/validate.sh   # a published image
#   bench/gpu/validate.sh -source /work/clip.mp4 -codecs h264,hevc  # gpuval options
#
# Content is synthetic (ffmpeg lavfi) unless -source names a public clip
# copied into the output directory, which is mounted at /work (see
# docs/gpu.md for download links).
set -eu

out=${QC_GPU_OUT:-gpu-validation}
image=${QC_IMAGE:-}

if [ -z "$image" ]; then
	image=qc:cuda
	docker build -f "$(dirname "$0")/../../Dockerfile.cuda" -t "$image" "$(dirname "$0")/../.."
fi

mkdir -p "$out"

docker run --rm --gpus all --user "$(id -u):$(id -g)" \
	-v "$(cd "$out" && pwd):/work" --workdir /work \
	--entrypoint gpuval "$image" -dir /work "$@"

echo "report: $out/gpu-report.md"
