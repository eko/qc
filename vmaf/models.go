package vmaf

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// defaultModelDirs are the install locations of libvmaf's model files.
var defaultModelDirs = []string{
	"/opt/homebrew/share/libvmaf/model",
	"/usr/local/share/libvmaf/model",
	"/usr/share/libvmaf/model",
}

// DefaultModelDirs returns the directories searched for model JSON files by
// default, in order: libvmaf's install locations (Homebrew, /usr/local,
// /usr).
func DefaultModelDirs() []string {
	return slices.Clone(defaultModelDirs)
}

// Evaluation resolutions: models are trained for a display resolution and
// both videos are scaled to it.
const (
	hdWidth, hdHeight   = 1920, 1080
	uhdWidth, uhdHeight = 3840, 2160
)

// hfrThreshold is the frame rate above which the high frame rate model is
// picked (above 30 fps, with room for 30000/1001).
const hfrThreshold = 30.5

var (
	// ErrModelNotFound is returned when a model name cannot be resolved.
	ErrModelNotFound = errors.New("model not found")
	// ErrUnknownDevice is returned for a device without a VMAF v1 model.
	ErrUnknownDevice = errors.New("unknown device")
)

// Devices: the viewing conditions VMAF v1 has a model for.
const (
	// DevicePhone is a phone held at 5 picture heights (vmaf_v1.0.16_5d0h).
	DevicePhone = "phone"
	// DeviceTV is a 1080p TV at 3 picture heights (vmaf_v1.0.16_3d0h), the
	// default model.
	DeviceTV = "tv"
	// Device4K is a 4K TV at 3 picture heights (vmaf_v1.0.16_3d0h_2160),
	// evaluated at 2160p.
	Device4K = "4k"
)

// devices lists the supported devices, in display order.
var devices = []string{DevicePhone, DeviceTV, Device4K}

// Devices returns the supported devices, in display order.
func Devices() []string {
	return slices.Clone(devices)
}

// deviceModels maps devices to their VMAF v1 model; the high frame rate
// variant inserts "hfr_" after the version.
var deviceModels = map[string]string{
	DevicePhone: "5d0h",
	DeviceTV:    "3d0h",
	Device4K:    "3d0h_2160",
}

// v1Prefix is the common prefix of the VMAF v1 model names.
const v1Prefix = "vmaf_v1.0.16_"

// DeviceModel returns the VMAF v1 model of a device, in its high frame rate
// variant above 30 fps (like the automatic model).
func DeviceModel(
	device string,
	fps float64,
) (string, error) {
	suffix, ok := deviceModels[device]
	if !ok {
		return "", fmt.Errorf("%w %q (supported: %s)", ErrUnknownDevice, device, strings.Join(devices, ", "))
	}

	if fps > hfrThreshold {
		return v1Prefix + "hfr_" + suffix, nil
	}

	return v1Prefix + suffix, nil
}

// ModelSpec identifies a model and the resolution it must be evaluated at.
type ModelSpec struct {
	Name string `json:"name"`
	// Source is a file path when FromPath is set, a built-in version otherwise.
	Source   string `json:"-"`
	FromPath bool   `json:"-"`
	Width    int    `json:"evalWidth"`
	Height   int    `json:"evalHeight"`
}

// ResolveModel picks the model to use. name may be "" or "auto" (VMAF v1:
// 4K model above 1080p, high frame rate variant above 30 fps), a model name
// found in dirs (e.g. "vmaf_v1.0.16_5d0h"), a path to a JSON model, or a
// libvmaf built-in version (e.g. "vmaf_v0.6.1").
func ResolveModel(
	name string,
	sourceHeight int,
	fps float64,
	dirs []string,
) (ModelSpec, error) {
	if name == "" || name == "auto" {
		name = autoModel(sourceHeight, fps)
	}

	if strings.HasSuffix(name, ".json") {
		if _, err := os.Stat(name); err != nil {
			return ModelSpec{}, fmt.Errorf("vmaf: %w: %w", ErrModelNotFound, err)
		}

		base := strings.TrimSuffix(filepath.Base(name), ".json")

		return spec(base, name, true), nil
	}

	for _, dir := range dirs {
		if path, ok := findModel(dir, name+".json"); ok {
			return spec(name, path, true), nil
		}
	}

	// Leave the final say to libvmaf's built-in models.
	return spec(name, name, false), nil
}

// autoModel is the VMAF v1 model matching the source: the 4K variant above
// 1080p, the high frame rate variant above 30 fps.
func autoModel(
	sourceHeight int,
	fps float64,
) string {
	device := DeviceTV
	if sourceHeight > hdHeight {
		device = Device4K
	}

	// Both devices exist.
	name, _ := DeviceModel(device, fps)

	return name
}

// spec infers the evaluation resolution from the model name: 4K models are
// evaluated at 2160p, every other model at 1080p (their training display).
func spec(
	name, source string,
	fromPath bool,
) ModelSpec {
	s := ModelSpec{Name: name, Source: source, FromPath: fromPath, Width: hdWidth, Height: hdHeight}

	if strings.Contains(name, "2160") || strings.Contains(name, "4k") {
		s.Width, s.Height = uhdWidth, uhdHeight
	}

	return s
}

// findModel searches dir recursively for file (models are installed in
// per-version subdirectories). Unreadable directories are skipped.
func findModel(
	dir, file string,
) (string, bool) {
	var found string

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing root or an unreadable directory: search elsewhere.
			return fs.SkipDir
		}

		if !d.IsDir() && d.Name() == file {
			found = path

			return fs.SkipAll
		}

		return nil
	})

	return found, found != ""
}
