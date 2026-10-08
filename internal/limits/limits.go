// Package limits tells how much CPU and memory a qc process may use: what
// the user asked for, or what its container allows.
package limits

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidMemory is returned for a malformed memory size.
var ErrInvalidMemory = errors.New("invalid memory size")

// Limits bounds the resources of a run. The zero value sets no limit.
type Limits struct {
	// CPUs is the number of CPUs used (0 = those of the machine, or of the
	// container's CPU quota).
	CPUs int
	// Memory is the memory the process and the tools it starts may use, in
	// bytes (0 = unknown: no limit is enforced).
	Memory int64
}

// Sizes of the binary units a memory size is written with.
const (
	kibibyte = 1 << 10
	mebibyte = 1 << 20
	gibibyte = 1 << 30
)

// ParseMemory reads a memory size as docker run --memory spells it: a
// number of bytes, or a number followed by k, m or g (binary units; "4g",
// "512m", "1.5g", also "4gb" and "4gib"). An empty string is 0, no limit.
func ParseMemory(
	s string,
) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}

	number := strings.TrimRight(s, "bikmg")

	unit := int64(1)

	switch strings.TrimSuffix(strings.TrimSuffix(s[len(number):], "b"), "i") {
	case "":
	case "k":
		unit = kibibyte
	case "m":
		unit = mebibyte
	case "g":
		unit = gibibyte
	default:
		return 0, fmt.Errorf("%w %q: want bytes or a number with k, m or g (4g, 512m)", ErrInvalidMemory, s)
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
	if err != nil || value <= 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("%w %q: want bytes or a number with k, m or g (4g, 512m)", ErrInvalidMemory, s)
	}

	return int64(value * float64(unit)), nil
}

// Files holding the memory limit of the control group of the process, in
// a cgroup file system (/sys/fs/cgroup): version 2, then version 1.
var containerMemoryFiles = []string{"memory.max", "memory/memory.limit_in_bytes"}

// unlimitedMemory is the least value read as "no limit": cgroup v1 reports
// a huge number rounded to the page size instead of "max".
const unlimitedMemory = 1 << 60

// ContainerMemory is the memory limit of the container the process runs
// in, read from its cgroup file system (os.DirFS("/sys/fs/cgroup")), or 0
// when there is none: no container, no limit, or another system.
func ContainerMemory(
	cgroup fs.FS,
) int64 {
	for _, name := range containerMemoryFiles {
		content, err := fs.ReadFile(cgroup, name)
		if err != nil {
			continue
		}

		limit, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
		if err != nil || limit <= 0 || limit >= unlimitedMemory {
			return 0
		}

		return limit
	}

	return 0
}

// FormatMemory spells a size for messages: "4.0 GiB", "512 MiB".
func FormatMemory(
	bytes int64,
) string {
	if bytes >= gibibyte {
		return strconv.FormatFloat(float64(bytes)/gibibyte, 'f', 1, 64) + " GiB"
	}

	return strconv.FormatInt(bytes/mebibyte, 10) + " MiB"
}

// Threads is the thread cap handed to the tools qc starts (ffmpeg decoders
// and encoders), which size themselves on the cores of the machine whatever
// the process may use: CPUs when set, else procs when the Go runtime runs
// on fewer processors than the machine has cores (runtime.GOMAXPROCS(0)
// against runtime.NumCPU(): a container CPU quota), else 0, no cap.
func (l Limits) Threads(
	procs, cores int,
) int {
	switch {
	case l.CPUs > 0:
		return l.CPUs
	case procs < cores:
		return procs
	}

	return 0
}
