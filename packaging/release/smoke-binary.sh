#!/bin/sh
# Smoke-tests a release binary where only ffmpeg is installed: it scores
# the VMAF of an encode with no model file around, so with the VMAF v1 model
# built into it.
#
#   packaging/release/smoke-binary.sh <path to qc>
set -eu

qc=$1
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

"$qc" version

ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc2=size=320x180:rate=25:duration=1 \
	-c:v libx264 -crf 10 -pix_fmt yuv420p "$work/source.mp4"
ffmpeg -hide_banner -loglevel error -i "$work/source.mp4" -c:v libx264 -crf 40 "$work/encode.mp4"

mkdir "$work/no-models"
"$qc" vmaf "$work/source.mp4" "$work/encode.mp4" --exact --model-dir "$work/no-models" \
	-f json -o "$work/vmaf.json" >/dev/null 2>&1
grep -q '"name": "vmaf_v1.0.16_3d0h"' "$work/vmaf.json"
echo "VMAF v1 scored with the built-in model"
