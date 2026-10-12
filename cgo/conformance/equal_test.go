//go:build cgo

package conformance

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/crhntr/hdf5/go/h5"
)

// diffValues compares two canonical value lists (h5/value.go) and returns
// "" when they are identical, else a description of the first difference
// with the path of the differing element ("[3].field[1]"). Floating point
// values are compared by bit pattern, so NaN payloads must match and 0 and
// -0 differ.
func diffValues(want, got []any) string {
	if len(want) != len(got) {
		return fmt.Sprintf("length: libhdf5 %d, hdf5 %d", len(want), len(got))
	}
	for i := range want {
		if d := diffValue(fmt.Sprintf("[%d]", i), want[i], got[i]); d != "" {
			return d
		}
	}
	return ""
}

func diffValue(path string, want, got any) string {
	differ := func() string {
		return fmt.Sprintf("%s: libhdf5 %s, hdf5 %s", path, short(want), short(got))
	}
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		return fmt.Sprintf("%s: libhdf5 %T %s, hdf5 %T %s", path, want, short(want), got, short(got))
	}
	switch w := want.(type) {
	case nil:
		return ""
	case []any:
		g := got.([]any)
		if len(w) != len(g) {
			return fmt.Sprintf("%s: length: libhdf5 %d, hdf5 %d", path, len(w), len(g))
		}
		for i := range w {
			if d := diffValue(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); d != "" {
				return d
			}
		}
		return ""
	case h5.Compound:
		g := got.(h5.Compound)
		if len(w.Fields) != len(g.Fields) {
			return fmt.Sprintf("%s: %d fields in libhdf5, %d in hdf5", path, len(w.Fields), len(g.Fields))
		}
		for i := range w.Fields {
			if w.Fields[i].Name != g.Fields[i].Name {
				return fmt.Sprintf("%s: field %d: libhdf5 %q, hdf5 %q", path, i, w.Fields[i].Name, g.Fields[i].Name)
			}
			if d := diffValue(path+"."+w.Fields[i].Name, w.Fields[i].Value, g.Fields[i].Value); d != "" {
				return d
			}
		}
		return ""
	case float32:
		if math.Float32bits(w) != math.Float32bits(got.(float32)) {
			return fmt.Sprintf("%s: libhdf5 %v (%#08x), hdf5 %v (%#08x)", path, w, math.Float32bits(w), got, math.Float32bits(got.(float32)))
		}
		return ""
	case float64:
		if math.Float64bits(w) != math.Float64bits(got.(float64)) {
			return fmt.Sprintf("%s: libhdf5 %v (%#016x), hdf5 %v (%#016x)", path, w, math.Float64bits(w), got, math.Float64bits(got.(float64)))
		}
		return ""
	case []byte:
		if !bytes.Equal(w, got.([]byte)) {
			return differ()
		}
		return ""
	}
	if reflect.TypeOf(want).Comparable() {
		if want != got {
			return differ()
		}
		return ""
	}
	if !reflect.DeepEqual(want, got) {
		return differ()
	}
	return ""
}

// short formats a value for a mismatch report, truncated.
func short(v any) string {
	s := fmt.Sprintf("%#v", v)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func TestDiffValues(t *testing.T) {
	nan1 := math.Float64frombits(0x7ff8000000000001)
	nan2 := math.Float64frombits(0x7ff8000000000002)
	assert.Empty(t, diffValues([]any{nan1, float32(1)}, []any{nan1, float32(1)}))
	assert.Contains(t, diffValues([]any{nan1}, []any{nan2}), "[0]")
	assert.Contains(t, diffValues([]any{0.0}, []any{math.Copysign(0, -1)}), "[0]")
	assert.Contains(t, diffValues([]any{int32(1)}, []any{int64(1)}), "int32")
	assert.Contains(t, diffValues([]any{1}, []any{1, 2}), "length")
	c := func(v any) h5.Compound { return h5.Compound{Fields: []h5.Field{{Name: "a", Value: []any{"x", v}}}} }
	assert.Empty(t, diffValues([]any{c(uint8(1))}, []any{c(uint8(1))}))
	assert.Equal(t, `[0].a[1]: libhdf5 0x1, hdf5 0x2`, diffValues([]any{c(uint8(1))}, []any{c(uint8(2))}))
	assert.Empty(t, diffValues([]any{[]byte{1}, h5.Enum{Name: "A"}, nil}, []any{[]byte{1}, h5.Enum{Name: "A"}, nil}))
	rr := h5.RegionRef{Selection: h5.Selection{Type: h5.SelPoints, Points: [][]uint64{{1}}}}
	assert.Empty(t, diffValues([]any{rr}, []any{rr}))
	rr2 := rr
	rr2.Selection.Points = [][]uint64{{2}}
	assert.NotEmpty(t, diffValues([]any{rr}, []any{rr2}))
}
