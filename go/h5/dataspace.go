package h5

// SpaceClass mirrors H5S_class_t.
type SpaceClass uint8

const (
	SpaceScalar SpaceClass = 0
	SpaceSimple SpaceClass = 1
	SpaceNull   SpaceClass = 2
)

// Unlimited is the maximum-dimension value meaning "unlimited" (H5S_UNLIMITED).
const Unlimited = ^uint64(0)

// Dataspace is a decoded dataspace message (the extent only).
type Dataspace struct {
	Class   SpaceClass
	Version uint8 // dataspace message version (1 or 2); 0 when unknown
	Dims    []uint64
	MaxDims []uint64 // nil when the message carried no max dims
}

// NumElems returns the number of elements selected by the whole extent:
// 1 for scalar, 0 for null, product of Dims otherwise.
func (s Dataspace) NumElems() uint64 {
	switch s.Class {
	case SpaceScalar:
		return 1
	case SpaceNull:
		return 0
	}
	n := uint64(1)
	for _, d := range s.Dims {
		n *= d
	}
	return n
}

// Rank returns the number of dimensions.
func (s Dataspace) Rank() int { return len(s.Dims) }

// Hyperslab is a regular hyperslab selection (start/stride/count/block per
// dimension, as in H5Sselect_hyperslab).
type Hyperslab struct {
	Start  []uint64
	Stride []uint64 // nil means 1 in every dimension
	Count  []uint64
	Block  []uint64 // nil means 1 in every dimension
}
