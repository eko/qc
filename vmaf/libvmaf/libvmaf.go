// Package libvmaf binds libvmaf (cgo) to score pairs of decoded frames: it
// implements the scoring contract of package vmaf (vmaf.Models,
// vmaf.Scorer), and Engine plugs it into quality.Meter.
//
// One Scorer runs several VMAF models at once, such as the TV and phone
// models of vmaf.DeviceModel, plus extra libvmaf feature extractors (PSNR,
// PSNR-HVS, SSIM, MS-SSIM, CIEDE2000, CAMBI), all on the same pictures.
//
// Frames are copied into libvmaf-owned pictures in a single cgo call each;
// libvmaf then runs its feature extractors on its own thread pool. With
// vmaf.ScorerConfig.Backend set to vmaf.BackendCUDA, the model features
// are extracted on an NVIDIA GPU instead: see vmaf.Backend. That needs a
// binary built with the cuda tag (go build -tags cuda) against a libvmaf
// built with CUDA (-Denable_cuda=true); CUDABuilt reports it, InitCUDA
// checks the device, and Version names the libvmaf linked in.
package libvmaf

/*
#cgo pkg-config: libvmaf
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <libvmaf/libvmaf.h>

// qc_picture allocates a 4:2:0 picture of bpc bits and copies the given
// planes (strides ys and cs in bytes; 16-bit samples above 8 bits) into it.
static int qc_picture(VmafPicture *pic, unsigned bpc, unsigned w, unsigned h,
                      const uint8_t *y, unsigned ys,
                      const uint8_t *u, const uint8_t *v, unsigned cs) {
	int err = vmaf_picture_alloc(pic, VMAF_PIX_FMT_YUV420P, bpc, w, h);
	if (err) {
		return err;
	}

	const uint8_t *src[3] = {y, u, v};
	const unsigned stride[3] = {ys, cs, cs};
	for (int p = 0; p < 3; p++) {
		uint8_t *dst = pic->data[p];
		for (unsigned row = 0; row < pic->h[p]; row++) {
			memcpy(dst + row * pic->stride[p], src[p] + row * stride[p], pic->w[p] * (bpc > 8 ? 2 : 1));
		}
	}

	return 0;
}

static int qc_read(VmafContext *vmaf, VmafPicture *ref, VmafPicture *dist, unsigned index) {
	int err = vmaf_read_pictures(vmaf, ref, dist, index);
	if (err) {
		vmaf_picture_unref(ref);
		vmaf_picture_unref(dist);
	}

	return err;
}

static int qc_flush(VmafContext *vmaf) {
	return vmaf_read_pictures(vmaf, NULL, NULL, 0);
}

// qc_load_model loads a model under name: libvmaf stores predicted scores by
// model name, so models sharing a context need distinct names.
static int qc_load_model(VmafModel **model, const char *source, const char *name, int from_path) {
	VmafModelConfig cfg = {.name = name, .flags = VMAF_MODEL_FLAGS_DEFAULT};
	if (from_path) {
		return vmaf_model_load_from_path(model, &cfg, source);
	}

	return vmaf_model_load(model, &cfg, source);
}

// qc_use_feature registers a feature extractor with its default options.
static int qc_use_feature(VmafContext *vmaf, const char *name) {
	return vmaf_use_feature(vmaf, name, NULL);
}

// qc_cambi_encoded adds the encode-side options of CAMBI to opts: the
// resolution and bit depth the distorted video was encoded at, when known
// (width above 0).
static int qc_cambi_encoded(VmafFeatureDictionary **opts, unsigned w, unsigned h, unsigned bpc) {
	if (w == 0) {
		return 0;
	}

	char value[16];
	snprintf(value, sizeof(value), "%u", w);
	int err = vmaf_feature_dictionary_set(opts, "enc_width", value);
	snprintf(value, sizeof(value), "%u", h);
	err |= vmaf_feature_dictionary_set(opts, "enc_height", value);
	snprintf(value, sizeof(value), "%u", bpc);
	err |= vmaf_feature_dictionary_set(opts, "enc_bitdepth", value);

	return err;
}

// qc_model_cambi sets the CAMBI feature of a model up like qc_use_cambi
// does, so that both stay one extractor: what the distorted video was
// encoded at, and with source set the reference measured too. It does
// nothing for a model without CAMBI.
static int qc_model_cambi(VmafModel *model, unsigned w, unsigned h, unsigned bpc, int source) {
	VmafFeatureDictionary *opts = NULL;
	int err = qc_cambi_encoded(&opts, w, h, bpc);
	if (!err && source) {
		err = vmaf_feature_dictionary_set(&opts, "full_ref", "true");
	}
	if (err || !opts) {
		vmaf_feature_dictionary_free(&opts);
		return err;
	}

	return vmaf_model_feature_overload(model, "cambi", opts);
}

// qc_use_cambi registers CAMBI with the options of the VMAF v1 models, so
// that a v1 model and a CAMBI measurement share one extractor (libvmaf
// deduplicates extractors with equal options), with the encode-side
// options the models were given, and with source the reference measured
// too (cambi_source).
static int qc_use_cambi(VmafContext *vmaf, unsigned w, unsigned h, unsigned bpc, int source) {
	VmafFeatureDictionary *opts = NULL;
	int err = vmaf_feature_dictionary_set(&opts, "cambi_high_res_speedup", "1080");
	err |= vmaf_feature_dictionary_set(&opts, "cambi_max_val", "17");
	err |= vmaf_feature_dictionary_set(&opts, "cambi_vis_lum_threshold", "0.06");
	if (source) {
		err |= vmaf_feature_dictionary_set(&opts, "full_ref", "true");
	}
	err |= qc_cambi_encoded(&opts, w, h, bpc);
	if (err) {
		vmaf_feature_dictionary_free(&opts);
		return err;
	}

	err = vmaf_use_feature(vmaf, "cambi", opts);
	if (err) {
		vmaf_feature_dictionary_free(&opts);
	}

	return err;
}

static int qc_init(VmafContext **vmaf, unsigned threads) {
	VmafConfiguration cfg = {
		.log_level = VMAF_LOG_LEVEL_NONE,
		.n_threads = threads,
		.n_subsample = 1,
	};

	return vmaf_init(vmaf, cfg);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"unsafe"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/vmaf"
)

var (
	// ErrGeometry is returned when a frame does not match the scorer
	// geometry.
	ErrGeometry = errors.New("frame geometry mismatch")
	// ErrNoModel is returned when a scorer is created without a model.
	ErrNoModel = errors.New("no model")
)

// libvmafError converts a libvmaf return code (a negated errno) into an error
// matching the errno with errors.Is.
func libvmafError(
	rc int,
) error {
	if rc < 0 {
		return syscall.Errno(-rc)
	}

	return fmt.Errorf("error %d", rc)
}

// Version returns the libvmaf version string.
func Version() string {
	return C.GoString(C.vmaf_version())
}

// Model is a loaded VMAF model. It must be closed.
type Model struct {
	c *C.VmafModel
}

// LoadModel loads the model of spec: a JSON file (spec.FromPath) or a
// built-in version. The model is named after its file or version, so that
// several models can be scored in one context.
func LoadModel(
	spec vmaf.ModelSpec,
) (*Model, error) {
	cs := C.CString(spec.Source)
	defer C.free(unsafe.Pointer(cs))

	cname := C.CString(strings.TrimSuffix(filepath.Base(spec.Source), ".json"))
	defer C.free(unsafe.Pointer(cname))

	var (
		m    Model
		flag C.int
	)

	if spec.FromPath {
		flag = 1
	}

	if rc := C.qc_load_model(&m.c, cs, cname, flag); rc != 0 {
		return nil, fmt.Errorf("libvmaf: load model %q: %w", spec.Source, libvmafError(int(rc)))
	}

	return &m, nil
}

// Close releases the model.
func (m *Model) Close() {
	if m.c != nil {
		C.vmaf_model_destroy(m.c)
		m.c = nil
	}
}

// featureCAMBIModel is the name libvmaf's feature collector gives CAMBI
// with the options of the VMAF v1 models.
const featureCAMBIModel = "cambi_hrs_1080_cmxv_17_vlt_0.06"

// featureCAMBISource is the name it gives CAMBI on the reference, whatever
// the options: the extractor does not declare that output.
const featureCAMBISource = "cambi_source"

// cambiFeature is the name libvmaf's feature collector gives CAMBI with the
// options of the VMAF v1 models and of the encode enc: the options follow
// in the alphabetical order of their names (enc_bitdepth, enc_height,
// enc_width).
func cambiFeature(
	enc vmaf.Encoded,
) string {
	if enc.Width == 0 {
		return featureCAMBIModel
	}

	return fmt.Sprintf("%s_encbd_%d_ench_%d_encw_%d", featureCAMBIModel, enc.BitDepth, enc.Height, enc.Width)
}

// cambiMinSide is the size one side of the encode must reach for CAMBI to
// take it (libvmaf's CAMBI_MIN_WIDTH_HEIGHT).
const cambiMinSide = 216

// encodedOtherwise returns enc when it tells CAMBI something the frames do
// not: an encode of another resolution or bit depth than the width ×
// height frames of depth bits. Otherwise, or for an encode smaller than
// CAMBI accepts, it returns the zero value, and CAMBI is left with its
// defaults.
func encodedOtherwise(
	enc vmaf.Encoded,
	width, height, depth int,
) vmaf.Encoded {
	if enc.Width <= 0 || enc.Height <= 0 || enc.BitDepth <= 0 {
		return vmaf.Encoded{}
	}

	if enc.Width == width && enc.Height == height && enc.BitDepth == depth {
		return vmaf.Encoded{}
	}

	if enc.Width < cambiMinSide && enc.Height < cambiMinSide {
		return vmaf.Encoded{}
	}

	return enc
}

// feature is an output of an extractor: its name in vmaf.Scores.Features and in
// libvmaf's feature collector (which suffixes non-default options).
type feature struct {
	name, libvmaf string
}

// extractorFeatures lists the outputs read back for each extractor.
var extractorFeatures = map[vmaf.Extractor][]feature{
	vmaf.ExtractorPSNR: {
		{vmaf.FeaturePSNRY, vmaf.FeaturePSNRY},
		{vmaf.FeaturePSNRCb, vmaf.FeaturePSNRCb},
		{vmaf.FeaturePSNRCr, vmaf.FeaturePSNRCr},
	},
	vmaf.ExtractorPSNRHVS:     {{vmaf.FeaturePSNRHVS, vmaf.FeaturePSNRHVS}},
	vmaf.ExtractorSSIM:        {{vmaf.FeatureSSIM, vmaf.FeatureSSIM}},
	vmaf.ExtractorMSSSIM:      {{vmaf.FeatureMSSSIM, vmaf.FeatureMSSSIM}},
	vmaf.ExtractorCIEDE2000:   {{vmaf.FeatureCIEDE2000, vmaf.FeatureCIEDE2000}},
	vmaf.ExtractorCAMBI:       {{vmaf.FeatureCAMBI, featureCAMBIModel}},
	vmaf.ExtractorCAMBISource: {{vmaf.FeatureCAMBI, featureCAMBIModel}, {vmaf.FeatureCAMBISource, featureCAMBISource}},
}

// Scorer computes per-frame VMAF, with one or more models, and extra libvmaf
// features for one contiguous sequence of frame pairs. It implements
// vmaf.Scorer and is not safe for concurrent use.
type Scorer struct {
	ctx           *C.VmafContext
	models        []*Model
	features      []feature
	width, height int
	bitDepth      int
	// encoded is what CAMBI is told of the encode, the zero value when it
	// is the frames themselves.
	encoded vmaf.Encoded
	pushed  int
	// locked is set when New locked the goroutine to its OS thread (CUDA):
	// Close unlocks it.
	locked bool
}

var _ vmaf.Scorer = (*Scorer)(nil)

// New returns a Scorer measuring models and cfg. The models are scored on
// every pair, in this order, and must outlive the scorer; features shared by
// several models are extracted once.
func New(
	models []*Model,
	cfg vmaf.ScorerConfig,
) (*Scorer, error) {
	if len(models) == 0 {
		return nil, ErrNoModel
	}

	s := &Scorer{models: models, width: cfg.Width, height: cfg.Height, bitDepth: max(cfg.BitDepth, 8)}
	s.encoded = encodedOtherwise(cfg.Encoded, s.width, s.height, s.bitDepth)

	if rc := C.qc_init(&s.ctx, C.uint(max(cfg.Threads, 0))); rc != 0 {
		return nil, fmt.Errorf("libvmaf: init: %w", libvmafError(int(rc)))
	}

	if err := s.useBackend(cfg.Backend); err != nil {
		s.Close()

		return nil, err
	}

	for _, model := range models {
		enc, source := s.encoded, C.int(0)
		if slices.Contains(cfg.Extractors, vmaf.ExtractorCAMBISource) {
			source = 1
		}

		if rc := C.qc_model_cambi(model.c, C.uint(enc.Width), C.uint(enc.Height), C.uint(enc.BitDepth), source); rc != 0 {
			s.Close()

			return nil, fmt.Errorf("libvmaf: set cambi up: %w", libvmafError(int(rc)))
		}

		if rc := C.vmaf_use_features_from_model(s.ctx, model.c); rc != 0 {
			s.Close()

			return nil, s.modelError(cfg.Backend, libvmafError(int(rc)))
		}
	}

	for _, extractor := range cfg.Extractors {
		if err := s.use(extractor); err != nil {
			s.Close()

			return nil, err
		}
	}

	return s, nil
}

// useBackend imports a CUDA state into the context for BackendCUDA. The
// goroutine is locked to its OS thread until Close: libvmaf's CUDA calls
// then always run on the thread that set the device context up.
func (s *Scorer) useBackend(
	backend vmaf.Backend,
) error {
	switch backend {
	case vmaf.BackendCPU:
		return nil
	case vmaf.BackendCUDA:
		runtime.LockOSThread()
		s.locked = true

		return useCUDA(s.ctx)
	}

	return fmt.Errorf("libvmaf: %w %q: resolve it with vmaf.ResolveBackend", vmaf.ErrBackend, backend)
}

// modelError explains a model libvmaf refused: a feature without CUDA
// extractor on CUDA, a feature extractor the library was built without on
// the CPU.
func (s *Scorer) modelError(
	backend vmaf.Backend,
	err error,
) error {
	if backend == vmaf.BackendCUDA {
		return fmt.Errorf("libvmaf: use model features: %w (%w)", vmaf.ErrCUDAModel, err)
	}

	return fmt.Errorf("libvmaf: use model features: %w (%w)", vmaf.ErrModelFeatures, err)
}

// use registers an extractor and the outputs to read back.
func (s *Scorer) use(
	extractor vmaf.Extractor,
) error {
	features, ok := extractorFeatures[extractor]
	if !ok {
		return fmt.Errorf("libvmaf: %w %q", vmaf.ErrUnknownExtractor, extractor)
	}

	var rc C.int

	switch extractor {
	case vmaf.ExtractorCAMBI, vmaf.ExtractorCAMBISource:
		enc, source := s.encoded, C.int(0)
		features = []feature{{vmaf.FeatureCAMBI, cambiFeature(enc)}}

		if extractor == vmaf.ExtractorCAMBISource {
			source = 1
			features = append(features, feature{vmaf.FeatureCAMBISource, featureCAMBISource})
		}

		rc = C.qc_use_cambi(s.ctx, C.uint(enc.Width), C.uint(enc.Height), C.uint(enc.BitDepth), source)
	default:
		name := C.CString(string(extractor))
		rc = C.qc_use_feature(s.ctx, name)
		C.free(unsafe.Pointer(name))
	}

	if rc != 0 {
		return fmt.Errorf("libvmaf: use %s: %w", extractor, libvmafError(int(rc)))
	}

	s.features = append(s.features, features...)

	return nil
}

// Push scores the next pair. Frames must carry chroma, match the scorer
// geometry and bit depth, and are not retained: their planes are copied.
func (s *Scorer) Push(
	ref, dist *frame.Frame,
) error {
	if err := s.check(ref); err != nil {
		return err
	}

	if err := s.check(dist); err != nil {
		return err
	}

	var refPic, distPic C.VmafPicture

	if err := s.picture(&refPic, ref); err != nil {
		return err
	}

	if err := s.picture(&distPic, dist); err != nil {
		C.vmaf_picture_unref(&refPic)

		return err
	}

	if rc := C.qc_read(s.ctx, &refPic, &distPic, C.uint(s.pushed)); rc != 0 {
		return fmt.Errorf("libvmaf: read pictures %d: %w", s.pushed, libvmafError(int(rc)))
	}

	s.pushed++

	return nil
}

// Scores flushes the extractors and returns the score of the first model
// for each pushed pair. Like Collect, it can only be called once.
func (s *Scorer) Scores() ([]float64, error) {
	out, err := s.Collect()
	if err != nil {
		return nil, err
	}

	return out.VMAF[0], nil
}

// Collect flushes the extractors and returns every model score and feature
// value of each pushed pair. It can only be called once: a flushed scorer
// accepts no more pairs.
func (s *Scorer) Collect() (vmaf.Scores, error) {
	if rc := C.qc_flush(s.ctx); rc != 0 {
		return vmaf.Scores{}, fmt.Errorf("libvmaf: flush: %w", libvmafError(int(rc)))
	}

	out := vmaf.Scores{VMAF: make([][]float64, len(s.models)), Features: make(map[string][]float64, len(s.features))}

	for m, model := range s.models {
		scores := make([]float64, s.pushed)

		for i := range scores {
			var score C.double
			if rc := C.vmaf_score_at_index(s.ctx, model.c, &score, C.uint(i)); rc != 0 {
				return vmaf.Scores{}, fmt.Errorf("libvmaf: score at %d: %w", i, libvmafError(int(rc)))
			}

			scores[i] = float64(score)
		}

		out.VMAF[m] = scores
	}

	for _, f := range s.features {
		values, err := s.feature(f.libvmaf)
		if err != nil {
			return vmaf.Scores{}, err
		}

		out.Features[f.name] = values
	}

	return out, nil
}

// feature reads the per-frame values of a feature.
func (s *Scorer) feature(
	name string,
) ([]float64, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	values := make([]float64, s.pushed)

	for i := range values {
		var value C.double
		if rc := C.vmaf_feature_score_at_index(s.ctx, cname, &value, C.uint(i)); rc != 0 {
			return nil, fmt.Errorf("libvmaf: feature %s at %d: %w", name, i, libvmafError(int(rc)))
		}

		values[i] = float64(value)
	}

	return values, nil
}

// Close releases the context. A CUDA scorer must be closed by the
// goroutine that created it.
func (s *Scorer) Close() {
	if s.ctx != nil {
		C.vmaf_close(s.ctx)
		s.ctx = nil
	}

	if s.locked {
		runtime.UnlockOSThread()
		s.locked = false
	}
}

// check rejects frames libvmaf would misread: another size, no chroma, or
// samples of the wrong width.
func (s *Scorer) check(
	f *frame.Frame,
) error {
	wantBytes := 1
	if s.bitDepth > 8 {
		wantBytes = 2
	}

	if f.Luma.Width != s.width || f.Luma.Height != s.height || f.Cb.Pix == nil || max(f.Luma.BytesPerSample, 1) != wantBytes {
		return fmt.Errorf("libvmaf: %w: got %dx%d (chroma=%t, %d bytes/sample), want %dx%d at %d bits",
			ErrGeometry, f.Luma.Width, f.Luma.Height, f.Cb.Pix != nil, f.Luma.BytesPerSample, s.width, s.height, s.bitDepth)
	}

	return nil
}

// picture copies f into a newly allocated libvmaf picture, in one cgo call.
func (s *Scorer) picture(
	pic *C.VmafPicture,
	f *frame.Frame,
) error {
	rc := C.qc_picture(pic, C.uint(s.bitDepth), C.uint(s.width), C.uint(s.height),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(f.Luma.Pix))), C.uint(f.Luma.Stride),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(f.Cb.Pix))),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(f.Cr.Pix))), C.uint(f.Cb.Stride))
	if rc != 0 {
		return fmt.Errorf("libvmaf: allocate picture: %w", libvmafError(int(rc)))
	}

	return nil
}
