package bitstream

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// PacketReader streams the compressed packets of the primary video stream.
type PacketReader interface {
	ReadPackets(
		ctx context.Context,
		path string,
		fn func(media.Packet) error,
	) error
}

// commandLines runs a command and calls fn for every stdout line. It is
// ffexec.Lines in production and a fake in tests.
type commandLines func(
	ctx context.Context,
	bin string,
	args []string,
	fn func(line []byte) error,
) error

// FFprobeReader implements PacketReader with ffprobe. It only demuxes: no frame
// is decoded, which keeps it close to disk read speed.
type FFprobeReader struct {
	bin   string
	lines commandLines
}

// NewFFprobeReader returns a PacketReader backed by the given ffprobe binary.
func NewFFprobeReader(
	bin string,
) *FFprobeReader {
	return &FFprobeReader{bin: bin, lines: ffexec.Lines}
}

// ReadPackets implements PacketReader.
func (r *FFprobeReader) ReadPackets(
	ctx context.Context,
	path string,
	fn func(media.Packet) error,
) error {
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time,dts_time,duration_time,size,flags",
		"-of", "compact=p=0",
		path,
	}

	err := r.lines(ctx, r.bin, args, func(line []byte) error {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}

		pkt, err := ParsePacketLine(line)
		if err != nil {
			return err
		}

		return fn(pkt)
	})
	if err != nil {
		return fmt.Errorf("read packets %s: %w", path, err)
	}

	return nil
}

// ParsePacketLine parses one ffprobe compact line such as
// "pts_time=0.1|dts_time=0.0|duration_time=0.033|size=1234|flags=K__".
// Missing timestamps ("N/A") fall back to the other timestamp.
func ParsePacketLine(
	line []byte,
) (media.Packet, error) {
	var (
		pkt            media.Packet
		hasPTS, hasDTS bool
	)

	for field := range bytes.SplitSeq(line, []byte("|")) {
		key, value, ok := bytes.Cut(field, []byte("="))
		if !ok {
			continue
		}

		switch string(key) {
		case "pts_time":
			pkt.PTS, hasPTS = parseSeconds(value)
		case "dts_time":
			pkt.DTS, hasDTS = parseSeconds(value)
		case "duration_time":
			pkt.Duration, _ = parseSeconds(value)
		case "size":
			size, err := strconv.Atoi(string(value))
			if err != nil {
				return media.Packet{}, fmt.Errorf("parse packet size %q: %w", value, err)
			}

			pkt.Size = size
		case "flags":
			pkt.Keyframe = bytes.IndexByte(value, 'K') >= 0
		}
	}

	switch {
	case !hasPTS && !hasDTS:
		return media.Packet{}, fmt.Errorf("packet without timestamp: %q", line)
	case !hasPTS:
		pkt.PTS = pkt.DTS
	case !hasDTS:
		pkt.DTS = pkt.PTS
	}

	return pkt, nil
}

// parseSeconds parses a decimal number of seconds; ok is false for "N/A".
func parseSeconds(
	value []byte,
) (time.Duration, bool) {
	f, err := strconv.ParseFloat(string(value), 64)
	if err != nil {
		return 0, false
	}

	return time.Duration(f * float64(time.Second)), true
}
