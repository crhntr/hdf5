package h5

// Tests written from the mutants gremlins left alive in this package
// (see also the slice pairs of TestFingerprintDistinguishes).

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMutationConvertZeroToUnsigned: a signed zero converts to every
// unsigned type (toUint64 rejects negative values only, convert.go:234).
func TestMutationConvertZeroToUnsigned(t *testing.T) {
	assert.Equal(t, []uint8{0, 0, 0, 0}, conv[uint8](t, int8(0), int16(0), int32(0), int64(0)))
	assert.Equal(t, []uint64{0, 0}, conv[uint64](t, false, Enum{Name: "A", Value: 0}))
	convErr[uint64](t, int8(-1))
}

// countWriter counts the Write calls and the bytes written.
type countWriter struct{ calls, n int }

func (w *countWriter) Write(b []byte) (int, error) {
	w.calls++
	w.n += len(b)
	return len(b), nil
}

// TestMutationWriteCanonicalBuffers: WriteCanonical writes in chunks of
// about 4 KiB, not once per element (fingerprint.go:126).
func TestMutationWriteCanonicalBuffers(t *testing.T) {
	var w countWriter
	assert.NoError(t, WriteCanonical(&w, make([]float64, 10000)))
	assert.Equal(t, 1+1+8+8*10000, w.n)
	assert.LessOrEqual(t, w.calls, 1+w.n/4096)
}
