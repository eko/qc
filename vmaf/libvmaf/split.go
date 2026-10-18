package libvmaf

/*
#include <stdlib.h>
#include <libvmaf/libvmaf.h>

// qc_use_feature_opts registers the extractor name with the n options
// keys[i] = values[i], as a model would register it.
static int qc_use_feature_opts(VmafContext *vmaf, const char *name, const char **keys, const char **values, int n) {
	VmafFeatureDictionary *opts = NULL;
	for (int i = 0; i < n; i++) {
		int err = vmaf_feature_dictionary_set(&opts, keys[i], values[i]);
		if (err) {
			vmaf_feature_dictionary_free(&opts);
			return err;
		}
	}

	int err = vmaf_use_feature(vmaf, name, opts);
	if (err) {
		vmaf_feature_dictionary_free(&opts);
	}

	return err;
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"github.com/eko/qc/frame"
)

// The temporal feature of the VMAF v1 models is motion, read on a frame and
// its neighbours: a warm-up frame is pushed for it alone. Their other
// features (CAMBI, chroma, ADM: three quarters of the cost of a frame,
// CAMBI and chroma most of it) are spatial, and extracted only from the
// frames whose scores are read. The motion of those frames is computed in
// a context of its own, from every frame, and imported.

// modelFeature is a feature a model reads, and how its extractor is
// registered.
type modelFeature struct {
	// extractor is the libvmaf extractor providing it.
	extractor string
	// keys and values are its options, as the model gives them.
	keys, values []string
	// collected is the name of a temporal feature in libvmaf's feature
	// collector, empty for a spatial one.
	collected string
}

// splitExtractors are the extractors of the spatial features of the VMAF
// v1 models, by the prefix of the feature name a model reads.
var splitExtractors = map[string]string{
	"Cambi_feature_":           "cambi",
	"Speed_chroma_feature_":    "speed_chroma",
	"VMAF_integer_feature_adm": "adm",
}

// motion3Feature is the motion feature of the VMAF v1 models.
const motion3Feature = "VMAF_integer_feature_motion3_score"

// jsonModel is the part of a model file telling the features it reads.
type jsonModel struct {
	ModelDict struct {
		FeatureNames []string         `json:"feature_names"`
		FeatureOpts  []map[string]any `json:"feature_opts_dicts"`
	} `json:"model_dict"`
}

// splitFeatures reads the features of the model file at path, and how to
// extract them apart: nil when one of them is not known to be spatial, or
// to be the motion of the VMAF v1 models with no other option than its
// largest value, whose name in the feature collector is then known.
func splitFeatures(
	path string,
) []modelFeature {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var m jsonModel
	if json.Unmarshal(data, &m) != nil || len(m.ModelDict.FeatureNames) != len(m.ModelDict.FeatureOpts) {
		return nil
	}

	var out []modelFeature

	for i, name := range m.ModelDict.FeatureNames {
		f := modelFeature{}

		for _, key := range slices.Sorted(mapKeys(m.ModelDict.FeatureOpts[i])) {
			value, ok := optionValue(m.ModelDict.FeatureOpts[i][key])
			if !ok {
				return nil
			}

			f.keys, f.values = append(f.keys, key), append(f.values, value)
		}

		if name == motion3Feature {
			if len(f.keys) != 1 || f.keys[0] != "motion_max_val" {
				return nil
			}

			f.extractor, f.collected = "motion", "integer_motion3_mmxv_"+f.values[0]
		}

		for prefix, extractor := range splitExtractors {
			if f.collected == "" && strings.HasPrefix(name, prefix) {
				f.extractor = extractor
			}
		}

		if f.extractor == "" {
			return nil
		}

		out = append(out, f)
	}

	return out
}

// optionValue writes an option of a model as libvmaf's dictionaries take
// it.
func optionValue(
	v any,
) (string, bool) {
	switch v := v.(type) {
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), true
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	}

	return "", false
}

// mapKeys iterates over the keys of m.
func mapKeys(
	m map[string]any,
) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// useSplit registers, on the scorer's context, the spatial features of
// every model, and on a context of its own their motion.
func (s *Scorer) useSplit() error {
	// A local: the address of a field would hand cgo the whole scorer.
	var motion *C.VmafContext
	if rc := initContext(&motion, 1); rc != 0 {
		return fmt.Errorf("libvmaf: init motion: %w", libvmafError(int(rc)))
	}

	s.motion = motion

	for _, model := range s.models {
		for _, f := range model.features {
			ctx := s.ctx

			switch {
			case f.collected != "":
				ctx = s.motion

				if !slices.Contains(s.motionFeatures, f.collected) {
					s.motionFeatures = append(s.motionFeatures, f.collected)
				}
			case f.extractor == "cambi":
				// As the model was set up (qc_model_cambi): one extractor.
				if rc := useCAMBI(s.ctx, s.encoded, s.cambiSource); rc != 0 {
					return fmt.Errorf("libvmaf: use cambi: %w", libvmafError(int(rc)))
				}

				continue
			}

			if err := useFeatureOpts(ctx, f); err != nil {
				return err
			}
		}
	}

	return nil
}

// useFeatureOpts registers f on ctx with its options.
func useFeatureOpts(
	ctx *C.VmafContext,
	f modelFeature,
) error {
	name := C.CString(f.extractor)
	defer C.free(unsafe.Pointer(name))

	keys, values := cStrings(f.keys), cStrings(f.values)
	defer freeStrings(keys)
	defer freeStrings(values)

	var k, v **C.char
	if len(keys) > 0 {
		k, v = &keys[0], &values[0]
	}

	if rc := C.qc_use_feature_opts(ctx, name, k, v, C.int(len(keys))); rc != 0 {
		return fmt.Errorf("libvmaf: use %s: %w", f.extractor, libvmafError(int(rc)))
	}

	return nil
}

func cStrings(
	ss []string,
) []*C.char {
	out := make([]*C.char, len(ss))
	for i, s := range ss {
		out[i] = C.CString(s)
	}

	return out
}

func freeStrings(
	cs []*C.char,
) {
	for _, c := range cs {
		C.free(unsafe.Pointer(c))
	}
}

// PushWarm pushes a warm-up pair: a neighbour of the scored frames, seen
// by the temporal features alone; its own values are NaN. A scorer that
// cannot split its features scores it in full.
func (s *Scorer) PushWarm(
	ref, dist *frame.Frame,
) error {
	if s.motion == nil {
		return s.Push(ref, dist)
	}

	if err := s.pushTo(s.motion, ref, dist, s.pushed); err != nil {
		return err
	}

	s.mainIndex = append(s.mainIndex, -1)
	s.pushed++

	return nil
}

// pushTo copies the pair into ctx at index.
func (s *Scorer) pushTo(
	ctx *C.VmafContext,
	ref, dist *frame.Frame,
	index int,
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

	if rc := readPictures(ctx, &refPic, &distPic, index); rc != 0 {
		return fmt.Errorf("libvmaf: read pictures %d: %w", index, libvmafError(int(rc)))
	}

	return nil
}

// importMotion flushes the motion context and imports, into the scorer's
// context, the motion of every scored pair.
func (s *Scorer) importMotion() error {
	if rc := flushContext(s.motion); rc != 0 {
		return fmt.Errorf("libvmaf: flush motion: %w", libvmafError(int(rc)))
	}

	for _, name := range s.motionFeatures {
		cname := C.CString(name)

		for i, j := range s.mainIndex {
			if j < 0 {
				continue
			}

			var value C.double
			if rc := C.vmaf_feature_score_at_index(s.motion, cname, &value, C.uint(i)); rc != 0 {
				C.free(unsafe.Pointer(cname))

				return fmt.Errorf("libvmaf: motion at %d: %w", i, libvmafError(int(rc)))
			}

			if rc := C.vmaf_import_feature_score(s.ctx, cname, value, C.uint(j)); rc != 0 {
				C.free(unsafe.Pointer(cname))

				return fmt.Errorf("libvmaf: import motion at %d: %w", j, libvmafError(int(rc)))
			}
		}

		C.free(unsafe.Pointer(cname))
	}

	return nil
}

// spread lays values of the scored pairs out over every pushed pair, NaN
// for the warm-up ones.
func (s *Scorer) spread(
	scored []float64,
) []float64 {
	out := make([]float64, len(s.mainIndex))

	for i, j := range s.mainIndex {
		out[i] = math.NaN()
		if j >= 0 {
			out[i] = scored[j]
		}
	}

	return out
}
