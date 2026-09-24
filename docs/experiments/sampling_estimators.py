#!/usr/bin/env python3
"""Compare estimators of the mean VMAF of a clip from a sample of frames.

This is the exploratory experiment behind the sampling design (see
docs/research.md, section 3). It compares, over many random draws:

- uniform random sampling of frames,
- stratified sampling (the same number of frames in every shot),
- both, corrected by a control variate: a cheap metric (PSNR or XPSNR)
  known on every frame, regressed on VMAF over the sample.

Inputs, produced from a reference and a distorted video of equal length:

    vmaf -r ref.y4m -d dist.y4m --json -o vmaf.json
    ffmpeg -i dist.mp4 -i ref.y4m -lavfi "[0:v][1:v]psnr=stats_file=psnr.log" -f null -
    ffmpeg -i dist.mp4 -i ref.y4m -lavfi "[0:v][1:v]xpsnr=stats_file=xpsnr.log" -f null -

Usage:

    python3 sampling_estimators.py vmaf.json psnr.log xpsnr.log --shots 6

Shots are assumed to be of equal length (the synthetic test clip is made of
6 shots of 150 frames).
"""

import argparse
import json
import math
import random
import re
import statistics

# PSNR of identical frames is infinite: cap it so regressions stay finite.
PSNR_CAP = 60.0
DRAWS = 2000


def read_vmaf(path):
    with open(path) as f:
        return [frame["metrics"]["vmaf"] for frame in json.load(f)["frames"]]


def read_log(path, pattern):
    values = []
    with open(path) as f:
        for line in f:
            match = re.search(pattern, line)
            if match:
                values.append(min(float(match.group(1)), PSNR_CAP))
    return values


def correlation(a, b):
    ma, mb = statistics.mean(a), statistics.mean(b)
    cov = sum((x - ma) * (y - mb) for x, y in zip(a, b))
    return cov / math.sqrt(sum((x - ma) ** 2 for x in a) * sum((y - mb) ** 2 for y in b))


def draw(n, shots, stratified, total):
    """Frame indices: n uniform frames, or n/len(shots) frames per shot."""
    if not stratified:
        return random.sample(range(total), n)
    per_shot = n // len(shots)
    return [i for shot in shots for i in random.sample(shot, per_shot)]


def regression_estimate(indices, vmaf, cheap):
    """Sample mean of VMAF, corrected by the cheap metric known everywhere."""
    sample = [vmaf[i] for i in indices]
    mean = statistics.mean(sample)
    if cheap is None:
        return mean

    covariate = [cheap[i] for i in indices]
    covariate_mean = statistics.mean(covariate)
    variance = sum((c - covariate_mean) ** 2 for c in covariate)
    if variance == 0:
        return mean

    beta = sum((c - covariate_mean) * (y - mean) for c, y in zip(covariate, sample)) / variance
    return mean + beta * (statistics.mean(cheap) - covariate_mean)


def within_strata_estimate(n, vmaf, cheap, shots):
    """Stratified estimate with one regression slope pooled within shots."""
    per_shot = n // len(shots)
    parts, dy, dc = [], [], []

    for shot in shots:
        indices = random.sample(shot, per_shot)
        sample = [vmaf[i] for i in indices]
        covariate = [cheap[i] for i in indices]
        sample_mean, covariate_mean = statistics.mean(sample), statistics.mean(covariate)
        parts.append((sample_mean, covariate_mean, statistics.mean(cheap[i] for i in shot)))
        dy += [y - sample_mean for y in sample]
        dc += [c - covariate_mean for c in covariate]

    variance = sum(c * c for c in dc)
    beta = sum(a * b for a, b in zip(dy, dc)) / variance if variance else 0
    return statistics.mean(m + beta * (full - c) for m, c, full in parts)


def report(label, errors):
    rmse = math.sqrt(statistics.mean(e * e for e in errors))
    p95 = sorted(abs(e) for e in errors)[int(0.95 * len(errors))]
    print(f"{label:40s} RMSE={rmse:5.2f}  |err| p95={p95:5.2f}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("vmaf")
    parser.add_argument("psnr")
    parser.add_argument("xpsnr")
    parser.add_argument("--shots", type=int, default=6)
    parser.add_argument("--seed", type=int, default=1)
    args = parser.parse_args()

    random.seed(args.seed)

    vmaf = read_vmaf(args.vmaf)
    psnr = read_log(args.psnr, r"psnr_y:(\S+)")
    xpsnr = read_log(args.xpsnr, r"^n:.*XPSNR y:\s*(\S+)")

    total = len(vmaf)
    truth = statistics.mean(vmaf)
    length = total // args.shots
    shots = [list(range(i * length, (i + 1) * length)) for i in range(args.shots)]

    print(f"frames {total}, true mean {truth:.3f}")
    print(f"correlation with VMAF: PSNR {correlation(vmaf, psnr):.3f}, XPSNR {correlation(vmaf, xpsnr):.3f}\n")

    for n in (12, 30, 60, 90):
        for stratified in (False, True):
            for name, cheap in (("plain", None), ("control PSNR", psnr), ("control XPSNR", xpsnr)):
                errors = [regression_estimate(draw(n, shots, stratified, total), vmaf, cheap) - truth
                          for _ in range(DRAWS)]
                mode = "stratified" if stratified else "uniform"
                report(f"n={n:3d} ({100 * n / total:4.1f}%) {mode} {name}", errors)

    print("\nstratified, control variate slope pooled within shots")
    for n in (12, 30, 60):
        for name, cheap in (("PSNR", psnr), ("XPSNR", xpsnr)):
            errors = [within_strata_estimate(n, vmaf, cheap, shots) - truth for _ in range(DRAWS)]
            report(f"n={n:3d} {name}", errors)


if __name__ == "__main__":
    main()
