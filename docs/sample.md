# Samples

`qc sample <source>... --to sample.mkv` extracts a **sample** of one or
several videos into one file: scenes of each, chosen on their content,
copied without re-encoding. It is what to hand an encoder, a codec trial or
a colleague instead of hours of mezzanine: a minute that is the hardest of
the programme, or that looks like it on average.

```sh
qc sample episode-01.mov episode-02.mov episode-03.mov --to sample.mkv
qc sample film.mp4 --to hardest.mp4 --duration 30 --scenes top
qc sample a.mov b.mov --to mix.mov --duration 120 --scenes mixed --top-share 0.3
```

## Which scenes

Every video is analysed (spatial and temporal information of each frame,
ITU-T P.910, the frame analysis of `qc analyze` without the camera motion)
and cut into **scenes**: runs of whole GOPs, at least `--piece` long (2 s).
`--scenes` picks among them:

| `--scenes` | Takes | Use it for |
|---|---|---|
| `top` | the scenes with the highest SI × TI | stress tests: what costs an encoder most, as the ladder's `--digest top` |
| `mixed` (default) | the most complex scenes for `--top-share` of the sample (a half), representative ones for the rest | a sample that shows the worst without being only the worst |
| `average` | scenes spread over the video that together have its SI and TI | a short stand-in for the video |
| `easy` | the scenes with the lowest SI × TI | static shots, credits, the floor of a bitrate |

- **Each video gives the same length**: `--duration` (60 s) is shared
  equally, whatever the videos' own lengths. A video no longer than its
  share is taken whole, and not analysed.
- **Complex and easy scenes** are taken in the order of their score until
  the share is reached; a scene is passed over when it would take the
  total further from the share than it is, so that one long GOP does not
  double the sample.
- **Representative scenes** are one per part of the video (as many parts
  as scenes are needed), each moved inside its part while the frames taken
  get closer to the video's mean SI and TI, and their length to the share.
  In a mixed sample they are chosen among what the complex scenes left,
  for the SI and TI of the whole video.
- **The sample plays the most complex scene first** when complex scenes
  are asked for, whatever video it comes from, then the next, down to the
  least complex taken; `easy` starts with the easiest. A mixed sample
  plays its complex scenes first, in that order, then its representative
  ones, which follow the order of the videos like those of `average`. The
  report lists the scenes as the sample plays them (`order` in the JSON),
  and per video in the video's own order (`sources[].segments`).

## Copied, not re-encoded

The scenes are **stream copies**: the packets of the source, from a
keyframe to the next. Nothing is decoded and encoded again, so the frames
of the sample are the sources' own, bit for bit, and a sample is written
in seconds. Three consequences:

- **About as long as asked.** A scene ends where a GOP ends. A source with
  10-second GOPs gives 10-second scenes; the report tells what was taken
  (59.5 to 62.3 s for 60 s asked on the three test titles).
- **The videos share their codec and format**: codec, resolution, frame
  rate, bit depth and dynamic range. Anything else is refused before any
  work, naming the two files and what differs. Encoding settings may
  differ: H.264 and HEVC scenes travel as MPEG-TS before they are joined,
  which carries the parameter sets of each source with its keyframes.
- **Video only.** The audio is left out.

The container is the one of `--to`'s extension; `.mkv` takes any codec.

## Read back

Once written, the sample is checked, and the report says what was found:

- its **frame count** against the scenes taken. A stream with open GOPs
  stores after a keyframe frames shown before it: cut there, they are lost;
- that **no two frames share a timestamp**, which a player resolves by
  dropping one;
- that **every frame decodes** (`ffmpeg -xerror -err_detect explode`).

A file that fails a check is still there: the finding tells why not to
trust it. On the test titles (H.264, closed GOPs), the decoded frames of
four samples were compared with those of the sources, scene by scene:
identical ([validation](validation.md#samples)).

## Cost

The frame analysis of the videos, then seconds: 20 s for a minute taken of
a 1-minute and a 10-minute title, 80 s when an hour-long title is among
them (its analysis). A video taken whole costs nothing.

## Library

```go
engine := sample.NewEngine(analyzer, ffmpeg) // *analysis.Analyzer, *encode.FFmpeg
res, err := engine.Extract(ctx, []string{"ep1.mov", "ep2.mov"}, "sample.mkv", sample.Options{
	Duration: media.Seconds(60),
	Scenes:   sample.ScenesMixed,
	TopShare: 0.3,
})

for _, video := range res.Sources {
	fmt.Println(video.Path, video.Taken, len(video.Segments), video.Complexity.SI, video.Complexity.VideoSI)
}
fmt.Println(res.Check.OK(), res.Check.Note)
```

`Result.Sources[i].Segments` lists every scene (its interval in the video,
its frames, its SI and TI, and why it was taken): the JSON report (`-o`,
`-f json`) is what to keep to know what a sample is made of.

## Limits

- Scenes are GOPs, not shots: a GOP that spans a cut carries both sides.
- "Easiest" is the lowest SI × TI: black frames and stills come first.
- Open-GOP sources lose frames at the cuts; the check reports it, and such
  sources need a re-encoded sample, which qc does not make.
- SI × TI ranks what costs an encoder, not what looks worst once encoded
  (see the ladder's [top digest](ladder.md#a-digest-of-the-most-complex-scenes)).
