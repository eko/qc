#!/usr/bin/env python3
"""Draws the charts of docs/model.md and docs/ladder.md as SVG files.

    python3 docs/assets/model/charts.py

The numbers are the measurements of docs/validation.md, kept here so that
the charts are redrawn when they change; cartoon-grid.json is the grid of a
title measured (columns: height, CRF, bitrate, VMAF) and predicted without
any anchor by a model that had not seen it (bitrate, VMAF), and
corpus-curve.json the errors and the changes of models trained on more and
more titles of one corpus, and corpus-surface.json the mean, the lowest and
the highest VMAF and bitrate of the titles of that corpus at every point of
the grid.

Each file carries its own light and dark colours and its own surface, so
that it reads on any page.
"""
import json
import math
import os

HERE = os.path.dirname(os.path.abspath(__file__))

# Roles, light then dark: the chart never names a colour, only a role.
ROLES = {
    "surface": ("#fcfcfb", "#1a1a19"),
    "border": ("#e1e0d9", "#2c2c2a"),
    "ink": ("#0b0b0b", "#f0efec"),
    "ink2": ("#52514e", "#c3c2b7"),
    "muted": ("#898781", "#898781"),
    "grid": ("#e1e0d9", "#2c2c2a"),
    "axis": ("#c3c2b7", "#383835"),
    "quiet": ("#c3c2b7", "#52514e"),
    "s1": ("#2a78d6", "#3987e5"),
    "s2": ("#eb6834", "#d95926"),
    "s3": ("#1baf7a", "#199e70"),
    "s4": ("#eda100", "#c98500"),
    "s5": ("#e87ba4", "#d55181"),
    # One hue for a magnitude: more is darker on the light surface, lighter
    # on the dark one.
    "q1": ("#cde2fb", "#0d366b"),
    "q2": ("#9ec5f4", "#184f95"),
    "q3": ("#6da7ec", "#256abf"),
    "q4": ("#3987e5", "#3987e5"),
    "q5": ("#1c5cab", "#6da7ec"),
    "q6": ("#0d366b", "#b7d3f6"),
}

STYLE = (
    "<style>\n"
    + ":root{"
    + "".join(f"--{k}:{v[0]};" for k, v in ROLES.items())
    + "}\n@media (prefers-color-scheme: dark){:root{"
    + "".join(f"--{k}:{v[1]};" for k, v in ROLES.items())
    + "}}\n"
    + "text{font-family:system-ui,-apple-system,'Segoe UI',sans-serif;font-size:12px;fill:var(--ink2)}\n"
    + ".title{font-size:15px;font-weight:600;fill:var(--ink)}\n"
    + ".sub{font-size:12.5px;fill:var(--ink2)}\n"
    + ".head{font-size:13px;font-weight:600;fill:var(--ink)}\n"
    + ".val{fill:var(--ink);font-variant-numeric:tabular-nums}\n"
    + ".tick{fill:var(--muted);font-variant-numeric:tabular-nums}\n"
    + ".note{fill:var(--muted)}\n"
    + ".grid{stroke:var(--grid);stroke-width:1}\n"
    + ".axis{stroke:var(--axis);stroke-width:1}\n"
    + "</style>"
)


class SVG:
    def __init__(self, width, height, title, subtitle, description):
        self.w, self.h = width, height
        self.parts = [
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" width="{width}" height="{height}" role="img">',
            f"<title>{esc(title)}</title>",
            f"<desc>{esc(description)}</desc>",
            STYLE,
            f'<rect x="0.5" y="0.5" width="{width - 1}" height="{height - 1}" rx="8" fill="var(--surface)" stroke="var(--border)"/>',
            f'<text class="title" x="24" y="32">{esc(title)}</text>',
            f'<text class="sub" x="24" y="52">{esc(subtitle)}</text>',
        ]

    def add(self, s):
        self.parts.append(s)

    def text(self, x, y, s, cls="", anchor="start", extra=""):
        c = f' class="{cls}"' if cls else ""
        self.add(f'<text x="{x:.1f}" y="{y:.1f}" text-anchor="{anchor}"{c}{extra}>{esc(s)}</text>')

    def line(self, x1, y1, x2, y2, cls="grid"):
        self.add(f'<line x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" class="{cls}"/>')

    def save(self, name):
        path = os.path.join(HERE, name)
        with open(path, "w") as f:
            f.write("\n".join(self.parts) + "\n</svg>\n")
        print("wrote", os.path.relpath(path))


def esc(s):
    return str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def bar(svg, x, y, length, thick, role):
    """A horizontal bar from a baseline at x: square there, rounded at its end."""
    r = min(4, length / 2)
    svg.add(
        f'<path d="M{x:.1f},{y:.1f} h{length - r:.1f} a{r},{r} 0 0 1 {r},{r} v{thick - 2 * r:.1f} '
        f'a{r},{r} 0 0 1 -{r},{r} h-{length - r:.1f} z" fill="var(--{role})"/>'
    )


# ------------------------------------------------- 1. what is measured

def chart_grid():
    heights = [1080, 720, 540, 360, 270]
    crfs = [13, 16.5, 20, 23.5, 27, 30.5, 34, 37.5, 41, 44.5, 48]
    panels = [
        ("Fixed probing", "no model", {(h, c) for h in heights for c in (20, 27, 34)}, "interpolated",
         "15 to 30 encodes", "3 min 36 s"),
        ("Model, 4 anchors", "--ladder-model", {(1080, 23.5), (1080, 41), (540, 30.5), (270, 23.5)}, "predicted",
         "4 encodes", "26 s"),
        ("Instant", "--ladder-model --instant", set(), "predicted", "no encode of the target encoder", "4 s"),
    ]
    svg = SVG(900, 372, "What is measured, what is predicted",
              "The grid of a title: every resolution at every CRF. A ladder is placed on the curves this grid draws.",
              "Three grids of 5 resolutions by 11 CRFs. Fixed probing measures 15 points and interpolates; a model with 4 "
              "anchors measures 4 points and predicts 51; instant measures none and predicts 55 from the analysis.")
    pw, left, top, dx, dy = 250, 74, 112, 19, 26
    for p, (name, flag, measured, rest, cost, time) in enumerate(panels):
        x0 = left + p * (pw + 28)
        svg.text(x0 - 6, 84, name, "head")
        svg.text(x0 - 6, 100, flag, "note")
        for i, h in enumerate(heights):
            y = top + 20 + i * dy
            if p == 0:
                svg.text(left - 20, y + 4, f"{h}p", "tick", "end")
            for j, c in enumerate(crfs):
                x = x0 + j * dx
                if (h, c) in measured:
                    svg.add(f'<circle cx="{x}" cy="{y}" r="6" fill="var(--s1)" stroke="var(--surface)" stroke-width="2"/>')
                else:
                    svg.add(f'<circle cx="{x}" cy="{y}" r="2.5" fill="var(--quiet)"/>')
        if p == 2:
            # The two very fast encodes of the analysis: not the target
            # encoder, not on the grid; drawn beside the 360p row.
            y = top + 20 + 3 * dy
            for j in (3, 7):
                x = x0 + j * dx
                svg.add(f'<rect x="{x - 5.5}" y="{y - 5.5}" width="11" height="11" rx="2" transform="rotate(45 {x} {y})" '
                        f'fill="var(--s2)" stroke="var(--surface)" stroke-width="2"/>')
        ybase = top + 20 + len(heights) * dy
        svg.text(x0, ybase + 2, "13", "tick", "middle")
        svg.text(x0 + 10 * dx, ybase + 2, "48", "tick", "middle")
        svg.text(x0 + 5 * dx, ybase + 2, "CRF", "tick", "middle")
        svg.text(x0 - 6, ybase + 30, cost, "val")
        svg.text(x0 - 6, ybase + 47, f"probing a 1 h title: {time}", "note")
    yl = 348
    svg.add(f'<circle cx="30" cy="{yl - 4}" r="6" fill="var(--s1)"/>')
    svg.text(42, yl, "encoded and scored with VMAF")
    svg.add(f'<circle cx="246" cy="{yl - 4}" r="2.5" fill="var(--quiet)"/>')
    svg.text(256, yl, "not encoded: interpolated, or predicted by the model")
    svg.add(f'<rect x="573.5" y="{yl - 9.5}" width="11" height="11" rx="2" transform="rotate(45 579 {yl - 4})" fill="var(--s2)"/>')
    svg.text(592, yl, "x264 ultrafast at 360p, scored: the micro-anchor")
    svg.save("grid.svg")


# --------------------------------------- 2. predicted against measured

def chart_curves():
    rows = json.load(open(os.path.join(HERE, "cartoon-grid.json")))
    heights = [1080, 720, 540, 360, 270]
    svg = SVG(900, 330, "An instant prediction against the truth",
              "A 10-minute cartoon the model had never seen: its curves predicted from the analysis alone, and as measured.",
              "Five small charts, one per resolution, of VMAF against bitrate. The line is the curve predicted without "
              "any encode, the dots the measured encodes. They agree within about one VMAF point; the predicted "
              "bitrates are 10 to 20 percent low.")
    lo, hi = math.log(120e3), math.log(14e6)
    y0, y1, pw, gap, left = 250, 100, 148, 20, 62

    def Y(v):
        return y0 - (v - 30) / 70 * (y0 - y1)

    for p, h in enumerate(heights):
        x0 = left + p * (pw + gap)

        def X(rate):
            return x0 + (math.log(rate) - lo) / (hi - lo) * pw

        svg.text(x0, 84, f"{h}p", "head")
        for v in (40, 60, 80, 100):
            svg.line(x0, Y(v), x0 + pw, Y(v))
            if p == 0:
                svg.text(x0 - 8, Y(v) + 4, v, "tick", "end")
        svg.line(x0, y0, x0 + pw, y0, "axis")
        for rate, label in ((200e3, "200k"), (1e6, "1M"), (5e6, "5M")):
            svg.line(X(rate), y0, X(rate), y0 + 4, "axis")
            svg.text(X(rate), y0 + 18, label, "tick", "middle")

        inside = lambda rate, v: 30 <= v <= 100 and math.exp(lo) <= rate <= math.exp(hi)
        pts = [r for r in rows if r[0] == h]
        path = " ".join(f"{X(r[4]):.1f},{Y(r[5]):.1f}" for r in pts if inside(r[4], r[5]))
        svg.add(f'<polyline fill="none" stroke="var(--s1)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" points="{path}"/>')
        for r in pts:
            if inside(r[2], r[3]):
                svg.add(f'<circle cx="{X(r[2]):.1f}" cy="{Y(r[3]):.1f}" r="4" fill="var(--s2)" stroke="var(--surface)" stroke-width="2"/>')
    svg.text(left - 8, y1 - 10, "VMAF", "tick", "end")
    svg.text(left + 5 * pw + 4 * gap, y0 + 38, "bitrate, bits per second (log scale)", "tick", "end")
    yl = 304
    svg.add(f'<line x1="{left}" y1="{yl - 4}" x2="{left + 26}" y2="{yl - 4}" stroke="var(--s1)" stroke-width="2" stroke-linecap="round"/>')
    svg.text(left + 34, yl, "predicted from the analysis, nothing encoded")
    svg.add(f'<circle cx="{left + 330}" cy="{yl - 4}" r="4" fill="var(--s2)"/>')
    svg.text(left + 342, yl, "measured: the title encoded at that point and scored")
    svg.save("curves.svg")


# ------------------------------------------- 3. error by what is measured

def chart_designs():
    designs = [
        ("The analysis without its two scored encodes", 3.62, "0 encode", False),
        ("The analysis alone: --instant", 1.16, "0 encode", True),
        ("1 anchor and the analysis", 1.02, "1 encode", False),
        ("2 anchors", 1.05, "2 encodes", False),
        ("3 anchors", 0.69, "3 encodes", False),
        ("4 anchors", 0.48, "4 encodes", False),
        ("7 anchors", 0.41, "7 encodes", False),
        ("10 anchors", 0.31, "10 encodes", False),
    ]
    svg = SVG(900, 396, "How wrong, for how much measuring",
              "Mean VMAF error on a title the model had never seen (three titles, each predicted by a model of the two others).",
              "Horizontal bars of the mean VMAF error by design: 3.62 for the analysis without its scored encodes, 1.16 "
              "for instant, 1.02 with one anchor, 1.05 with two, 0.69 with three, 0.48 with four, 0.41 with seven and "
              "0.31 with ten. The default tolerance is 0.5.")
    x0, top, step, scale = 330, 92, 32, 116
    for v in (0, 1, 2, 3, 4):
        svg.line(x0 + v * scale, top - 8, x0 + v * scale, top + len(designs) * step, "grid" if v else "axis")
        svg.text(x0 + v * scale, top + len(designs) * step + 18, v, "tick", "middle")
    svg.text(x0 + 4 * scale, top + len(designs) * step + 38, "VMAF points", "tick", "end")
    for i, (name, value, cost, accent) in enumerate(designs):
        y = top + i * step
        svg.text(x0 - 12, y + 15, name, "val" if accent else "", "end")
        bar(svg, x0, y + 3, value * scale, 16, "s1" if accent else "quiet")
        # A value never sits on the tolerance line.
        end = value * scale
        if end < 0.5 * scale < end + 40:
            end = 0.5 * scale
        svg.text(x0 + end + 8, y + 15, f"{value:.2f}", "val")
        svg.text(x0 + end + 44, y + 15, cost, "note")
    xt = x0 + 0.5 * scale
    svg.add(f'<line x1="{xt}" y1="{top - 8}" x2="{xt}" y2="{top + len(designs) * step}" stroke="var(--ink2)" stroke-width="1"/>')
    svg.text(xt + 6, top - 12, "default --tolerance 0.5: under it, the ladder needs nothing more", "note")
    svg.save("designs.svg")


# ----------------------------------------------- 4. the corpus growing

def chart_corpus():
    data = json.load(open(os.path.join(HERE, "corpus-curve.json")))
    titles = [r["Titles"] for r in data["instant"]]
    panels = [
        ("Bitrate error, title never seen", "RateError", 10, [0, 2, 4, 6, 8, 10], False),
        ("Change when a title is added: all rungs", "Change", 4, [0, 1, 2, 3, 4], True),
        ("Change when a title is added: top rung", "TopChange", 10, [0, 1, 2, 4, 6, 8, 10], True),
    ]
    first, last = data["instant"][0], data["instant"][-1]
    anchors = data["anchors4"][-1]
    svg = SVG(900, 420, "What more titles change",
              f"A corpus of {data['titles']} titles of 30 s: models trained on {titles[0]} to {titles[-1]} of them drawn at "
              "random, judged on five others.",
              "Three line charts against the number of titles in the corpus, for instant and for four anchors. The "
              f"bitrate error falls from {first['RateError']:.1f} to {last['RateError']:.1f} percent for instant and stays "
              f"near {anchors['RateError']:.1f} percent for four anchors. The mean change of a predicted bitrate when a title is added falls from "
              f"{first['Change']:.1f} to {last['Change']:.1f} percent for instant, and that of the top rung from "
              f"{first['TopChange']:.1f} to {last['TopChange']:.1f} percent.")
    lo, hi = titles[0], titles[-1]
    for p, (name, key, top_value, ticks, reference) in enumerate(panels):
        x0, x1, y0, y1 = 66 + p * 286, 286 + p * 286, 320, 110
        svg.text(x0 - 34, 86, name, "head")

        def X(k):
            return x0 + (k - lo) / (hi - lo) * (x1 - x0)

        def Y(v):
            return y0 - v / top_value * (y0 - y1)

        for v in ticks:
            svg.line(x0, Y(v), x1, Y(v), "grid" if v else "axis")
            svg.text(x0 - 8, Y(v) + 4, f"{v}%", "tick", "end")
        if reference:
            svg.add(f'<line x1="{x0}" y1="{Y(1):.1f}" x2="{x1}" y2="{Y(1):.1f}" stroke="var(--ink2)" stroke-width="1"/>')
        for k in range(5, hi + 1, 5):
            svg.text(X(k), y0 + 18, k, "tick", "middle")
        labels = []
        for series, role in (("instant", "s1"), ("anchors4", "s2")):
            values = [r[key] for r in data[series]]
            pts = " ".join(f"{X(k):.1f},{Y(v):.1f}" for k, v in zip(titles, values))
            svg.add(f'<polyline fill="none" stroke="var(--{role})" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" points="{pts}"/>')
            svg.add(f'<circle cx="{X(hi):.1f}" cy="{Y(values[-1]):.1f}" r="4" fill="var(--{role})" stroke="var(--surface)" stroke-width="2"/>')
            # The value of a line sits above its end; above the line a little
            # earlier where the line falls to its end.
            at = -5 if values[-5] > 1.3 * values[-1] else -1
            labels.append([Y(values[at]) - 9, f"{values[-1]:.1f}%"])
        # Two values never sit on each other, nor the lower one on the upper
        # line: both then stand above it.
        if labels[1][0] - labels[0][0] < 24:
            labels[1][0] = min(labels[1][0], labels[0][0])
        if labels[1][0] - labels[0][0] < 15:
            labels[0][0] = labels[1][0] - 15
        for y, text in labels:
            svg.text(X(hi) - 2, y, text, "val", "end")
    svg.text(66 + 2 * 286 + 220, 320 + 38, "titles in the corpus", "tick", "end")
    yl = 378
    svg.add(f'<line x1="34" y1="{yl - 4}" x2="60" y2="{yl - 4}" stroke="var(--s1)" stroke-width="2" stroke-linecap="round"/>')
    svg.text(68, yl, "instant: the analysis alone")
    svg.add(f'<line x1="264" y1="{yl - 4}" x2="290" y2="{yl - 4}" stroke="var(--s2)" stroke-width="2" stroke-linecap="round"/>')
    svg.text(298, yl, "4 anchors measured")
    svg.add(f'<line x1="470" y1="{yl - 4}" x2="496" y2="{yl - 4}" stroke="var(--ink2)" stroke-width="1"/>')
    svg.text(504, yl, "1%")
    svg.text(34, yl + 22, "More titles steady the predictions, about as one over their number, and bring the instant errors down slowly.", "note")
    svg.save("corpus.svg")


# -------------------------------------------------- 5. where the time goes

def chart_time():
    rows = [
        ("Fixed probing, every rung verified", 436, "7 min 16 s", "30 probes", False),
        ("Model with 4 anchors, every rung verified", 167, "2 min 47 s", "4 probes", False),
        ("Predicted, every rung verified", 129, "2 min 09 s", "no probe", False),
        ("Instant", 4, "4 s", "no probe, no verification", True),
    ]
    svg = SVG(900, 250, "The time of a ladder",
              "An hour-long 1080p title, x264 fast, Apple M2 Max; the model had never seen the title.",
              "Horizontal bars of the time of a ladder: 7 minutes 16 seconds with fixed probing, 2 minutes 47 with a "
              "model of 4 anchors, 2 minutes 9 predicted and verified, 4 seconds instant.")
    x0, top, step, scale = 330, 88, 34, 0.86
    for sec, label in ((0, "0"), (120, "2 min"), (240, "4 min"), (360, "6 min"), (480, "8 min")):
        svg.line(x0 + sec * scale, top - 8, x0 + sec * scale, top + len(rows) * step, "grid" if sec else "axis")
        svg.text(x0 + sec * scale, top + len(rows) * step + 18, label, "tick", "middle")
    for i, (name, seconds, label, cost, accent) in enumerate(rows):
        y = top + i * step
        svg.text(x0 - 12, y + 16, name, "val" if accent else "", "end")
        bar(svg, x0, y + 4, max(seconds * scale, 8), 16, "s1" if accent else "quiet")
        svg.text(x0 + max(seconds * scale, 8) + 8, y + 16, label, "val")
        svg.text(x0 + max(seconds * scale, 8) + 14 + 7 * len(label), y + 16, cost, "note")
    svg.save("time.svg")


# ------------------------------------------- 6. the corpus as a surface

def chart_surface():
    data = json.load(open(os.path.join(HERE, "corpus-surface.json")))
    heights, crfs = data["heights"], data["crfs"]
    svg = SVG(940, 530, "What a corpus holds: two surfaces over one grid",
              f"{data['titles']} titles of 30 s, each encoded at 5 resolutions × 11 CRFs. Height: the mean of the corpus. "
              "Bars: from its lowest title to its highest, at each of the 55 points.",
              "Two surfaces over the grid of resolutions by CRFs, both coloured by VMAF. Left, VMAF: near 100 at low "
              "CRF and high resolution, falling to under 5 at 270p and CRF 48. Right, bitrate on a log scale: from "
              "about 29 megabits per second at 1080p and CRF 13 to 40 kilobits at 270p and CRF 48. Vertical bars show "
              "how far titles differ at each point.")
    ax, ay, bx, by = 25, 7, 21, -15  # a step of CRF, a step of resolution

    def shade(vmaf):
        return "q%d" % (1 + min(5, int(vmaf // (100 / 6))))

    def panel(x0, y0, name, value, unit_ticks, zscale):
        svg.text(x0 - 46, 84, name, "head")

        def P(i, j, z):
            return x0 + i * ax + j * bx, y0 + i * ay + j * by - z * zscale

        # The floor of the grid, then its two labelled edges.
        floor = [P(0, 0, 0), P(len(crfs) - 1, 0, 0), P(len(crfs) - 1, len(heights) - 1, 0), P(0, len(heights) - 1, 0)]
        svg.add('<polygon points="%s" fill="none" class="axis"/>' % " ".join(f"{x:.1f},{y:.1f}" for x, y in floor))
        # Every CRF of the grid is named: the count is part of the picture.
        for i, c in enumerate(crfs):
            x, y = P(i, 0, 0)
            svg.line(x, y, x - 2, y + 5, "axis")
            svg.text(x - 5, y + 17, f"{c:g}", "tick", "middle", ' style="font-size:10.5px"')
        x, y = P(5, 0, 0)
        svg.text(x - 30, y + 40, f"CRF ({len(crfs)} values)", "tick", "middle")
        for j, h in enumerate(heights):
            x, y = P(len(crfs) - 1, j, 0)
            svg.text(x + 10, y + 4, f"{h}p", "tick")
        # The height axis, at the front left corner, where nothing hides it.
        xb, yb = P(0, 0, 0)
        top = max(z for _, z in unit_ticks)
        svg.line(xb, yb, xb, yb - top * zscale, "axis")
        for label, z in unit_ticks:
            svg.line(xb - 4, yb - z * zscale, xb, yb - z * zscale, "axis")
            svg.text(xb - 8, yb - z * zscale + 4, label, "tick", "end")

        # From the back row to the front one, so that nearer faces cover
        # farther ones; each face takes the mean VMAF of its corners.
        for j in range(len(heights) - 2, -1, -1):
            for i in range(len(crfs) - 1):
                corners = [(i, j), (i + 1, j), (i + 1, j + 1), (i, j + 1)]
                pts = " ".join("%.1f,%.1f" % P(a, b, value(a, b)[0]) for a, b in corners)
                vmaf = sum(data["vmaf"][b][a][0] for a, b in corners) / 4
                svg.add(f'<polygon points="{pts}" fill="var(--{shade(vmaf)})" stroke="var(--surface)" stroke-width="1" stroke-linejoin="round"/>')
        # How far the titles of the corpus are apart, at every point.
        for j in range(len(heights) - 1, -1, -1):
            for i in range(len(crfs)):
                mean, low, high = value(i, j)
                x, y1 = P(i, j, low)
                _, y2 = P(i, j, high)
                _, ym = P(i, j, mean)
                svg.add(f'<line x1="{x:.1f}" y1="{y1:.1f}" x2="{x:.1f}" y2="{y2:.1f}" stroke="var(--ink)" stroke-width="1.2" stroke-linecap="round"/>')
                svg.add(f'<circle cx="{x:.1f}" cy="{ym:.1f}" r="2.2" fill="var(--ink)" stroke="var(--surface)" stroke-width="1"/>')

    panel(64, 356, "VMAF", lambda i, j: data["vmaf"][j][i],
          [("0", 0), ("25", 25), ("50", 50), ("75", 75), ("100", 100)], 1.9)

    lo = math.log10(30e3)
    rate = lambda i, j: [math.log10(v) - lo for v in data["rate"][j][i]]
    panel(552, 356, "Bitrate (log scale)", rate,
          [("30 kb/s", 0), ("300 kb/s", 1), ("3 Mb/s", 2), ("30 Mb/s", 3)], 190 / 3.2)

    yl = 488
    svg.text(40, yl, "VMAF of the corpus, on both surfaces:")
    for k in range(6):
        svg.add(f'<rect x="{262 + k * 44}" y="{yl - 11}" width="42" height="12" fill="var(--q{k + 1})"/>')
        svg.text(262 + k * 44, yl + 16, f"{round(k * 100 / 6)}", "tick", "middle")
    svg.text(262 + 6 * 44, yl + 16, "100", "tick", "middle")
    svg.add(f'<line x1="580" y1="{yl - 12}" x2="580" y2="{yl + 2}" stroke="var(--ink)" stroke-width="1.5" stroke-linecap="round"/>')
    svg.add(f'<circle cx="580" cy="{yl - 5}" r="2.5" fill="var(--ink)"/>')
    svg.text(590, yl, "corpus mean, from its lowest to its highest title")
    svg.save("surface.svg")


if __name__ == "__main__":
    chart_surface()
    chart_grid()
    chart_curves()
    chart_designs()
    chart_corpus()
    chart_time()
