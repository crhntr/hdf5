package hdf5

// Tests for the attribute code (h5a_int.go, h5a_dense.go) written from the
// mutants gremlins left alive. The objects are assembled byte by byte with
// h5Builder (mutation_h5o_test.go) or come from testdata/attr_dense.h5;
// the expectations are what libhdf5 1.14.6 reads (H5Aiterate2 in every
// index and order) from the same bytes (set HDF5_MUTATION_DUMP to a
// directory to have the tests write the files there).

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attrMsg is a version 3 attribute message: scalar int32 attribute name
// of value v.
func attrMsg(name string, v int32) ohMsg {
	b := []byte{3, 0}
	b = binary.LittleEndian.AppendUint16(b, uint16(len(name)+1))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(i32Type().data)))
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = append(b, 0)
	b = append(b, name...)
	b = append(b, 0)
	b = append(b, i32Type().data...)
	b = append(b, 2, 0, 0, 0) /* scalar dataspace */
	b = binary.LittleEndian.AppendUint32(b, uint32(v))
	return ohMsg{typ: h5oAttrID, data: b}
}

// v2MsgsCrt encodes messages for a version 2 object header chunk whose
// messages have creation order fields (H5O_HDR_ATTR_CRT_ORDER_TRACKED):
// msgs[i] has creation index crt[i].
func v2MsgsCrt(msgs []ohMsg, crt []uint16) []byte {
	var b []byte
	for i, m := range msgs {
		b = append(b, byte(m.typ))
		b = binary.LittleEndian.AppendUint16(b, uint16(len(m.data)))
		b = append(b, m.flags)
		b = binary.LittleEndian.AppendUint16(b, crt[i])
		b = append(b, m.data...)
	}
	return b
}

// attrOrder iterates over the attributes of the object header at addr in
// the given index and order, as "name=value" strings.
func attrOrder(t *testing.T, f *File, addr uint64, idx h5aIndex, order h5aIterOrder) []string {
	t.Helper()
	oh, err := f.oProtect(addr)
	require.NoError(t, err)
	var got []string
	require.NoError(t, f.oAttrIterateReal(oh, idx, order, 0, nil, func(attr *h5oAttr) error {
		v, err := f.aRead(attr)
		require.NoError(t, err)
		got = append(got, fmt.Sprintf("%s=%v", attr.name, v[0]))
		return nil
	}))
	return got
}

// TestMutationAttrCompactCreationOrder: compact attributes of a version 2
// object header that tracks their creation order, stored out of creation
// order: H5A__compact_build_table keeps the stored creation indexes
// (h5a_int.go:228), and duplicated creation indexes and names keep the
// header order in both directions (h5a_int.go:338, 351, 389; libhdf5
// sorts with qsort, which glibc implements as a stable merge sort).
func TestMutationAttrCompactCreationOrder(t *testing.T) {
	type orders struct{ nameInc, nameDec, crtInc, crtDec []string }
	for _, c := range []struct {
		name string
		msgs []ohMsg
		crt  []uint16
		want orders
	}{
		{"out_of_order", []ohMsg{attrMsg("b", 1), attrMsg("a", 2), attrMsg("c", 3)}, []uint16{1, 0, 2}, orders{
			nameInc: []string{"a=2", "b=1", "c=3"},
			nameDec: []string{"c=3", "b=1", "a=2"},
			crtInc:  []string{"a=2", "b=1", "c=3"},
			crtDec:  []string{"c=3", "b=1", "a=2"},
		}},
		{"duplicates", []ohMsg{attrMsg("x", 1), attrMsg("y", 2), attrMsg("x", 3), attrMsg("w", 4)}, []uint16{5, 5, 5, 4}, orders{
			nameInc: []string{"w=4", "x=1", "x=3", "y=2"},
			nameDec: []string{"y=2", "x=1", "x=3", "w=4"},
			crtInc:  []string{"w=4", "x=1", "y=2", "x=3"},
			crtDec:  []string{"x=1", "y=2", "x=3", "w=4"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, obj := buildObject(t, func(b *h5Builder) uint64 {
				return b.put(ohdr2(2|h5oHdrAttrCrtOrderTracked, v2MsgsCrt(append([]ohMsg{linfoMsg()}, c.msgs...), append([]uint16{0}, c.crt...))))
			})
			assert.Equal(t, c.want, orders{
				nameInc: attrOrder(t, f, obj, h5aIndexName, h5aIterInc),
				nameDec: attrOrder(t, f, obj, h5aIndexName, h5aIterDec),
				crtInc:  attrOrder(t, f, obj, h5aIndexCrtOrder, h5aIterInc),
				crtDec:  attrOrder(t, f, obj, h5aIndexCrtOrder, h5aIterDec),
			})
		})
	}
}

// TestMutationAttrDenseEmpty: dense attribute storage whose name index
// holds no record and whose fractal heap address points at bytes that are
// not a heap: H5A__dense_build_table opens nothing else when the index is
// empty (h5a_int.go:291), and libhdf5 lists no attribute.
func TestMutationAttrDenseEmpty(t *testing.T) {
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		h := append([]byte("BTHD"), 0, h5b2AttrDenseNameID)
		h = binary.LittleEndian.AppendUint32(h, 512)
		h = binary.LittleEndian.AppendUint16(h, 17)
		h = binary.LittleEndian.AppendUint16(h, 0)
		h = append(h, 100, 40)
		h = le64a(binary.LittleEndian.AppendUint16(le64a(h, haddrUndef), 0), 0)
		h = binary.LittleEndian.AppendUint32(h, checksumLookup3(h, 0))
		name := b.put(h)
		notHeap := b.alloc(64)
		ainfo := ohMsg{typ: h5oAinfoID, data: le64a(le64a([]byte{0, 0}, notHeap), name)}
		return b.put(ohdr2(2, v2Msgs(linfoMsg(), ainfo)))
	})
	g, err := f.OpenGroup("/obj")
	require.NoError(t, err)
	n, err := g.NumAttributes()
	require.NoError(t, err)
	assert.Zero(t, n)
	for _, err := range g.Attributes() {
		require.NoError(t, err)
		t.Error("an attribute")
	}
}

// TestMutationAttrDenseNativeSkip: iteration over dense attributes in the
// native order of the name index, skipping the first 3, reports the index
// after the last attribute (h5a_dense.go:231): libhdf5's H5Aiterate2 on
// testdata/attr_dense.h5 /tracked (30 attributes) sets idx to 30.
func TestMutationAttrDenseNativeSkip(t *testing.T) {
	f := openTestdata(t, "attr_dense.h5")
	oh := ohAt(t, f, "/tracked")
	var last uint64
	seen := 0
	require.NoError(t, f.oAttrIterateReal(oh, h5aIndexName, h5aIterNative, 3, &last, func(*h5oAttr) error {
		seen++
		return nil
	}))
	assert.Equal(t, 27, seen)
	assert.Equal(t, uint64(30), last)
}

// TestMutationAttrCopy: h5aCopy returns an attribute that shares no
// memory with the original (h5a_int.go:133, 153, 172, 175, 178).
func TestMutationAttrCopy(t *testing.T) {
	member := &h5.Datatype{Class: h5.ClassInteger, Size: 4}
	orig := &h5oAttr{
		name: "a",
		dt: &h5.Datatype{Class: h5.ClassCompound, Members: []h5.Member{{Name: "m", Type: member}},
			EnumNames: []string{"x"}, EnumValues: []int64{1}, ArrayDims: []uint64{2},
			Base: &h5.Datatype{Class: h5.ClassEnum}},
		ds:       &h5sExtent{size: []uint64{2}, max: []uint64{3}},
		data:     []byte{1, 2},
		dtShared: &h5oShared{heapID: []byte{1}},
		dsShared: &h5oShared{heapID: []byte{2}},
		shared:   &h5oShared{heapID: []byte{3}},
	}
	c := h5aCopy(orig)
	require.Equal(t, orig, c)
	c.dt.Members[0].Type.Size = 8
	c.dt.Members[0].Name = "n"
	c.dt.EnumNames[0] = "y"
	c.dt.EnumValues[0] = 2
	c.dt.ArrayDims[0] = 3
	c.dt.Base.Size = 1
	c.ds.size[0] = 5
	c.ds.max[0] = 5
	c.data[0] = 9
	c.dtShared.heapID[0] = 9
	c.dsShared.heapID[0] = 9
	c.shared.heapID[0] = 9
	assert.Equal(t, &h5oAttr{
		name: "a",
		dt: &h5.Datatype{Class: h5.ClassCompound, Members: []h5.Member{{Name: "m", Type: &h5.Datatype{Class: h5.ClassInteger, Size: 4}}},
			EnumNames: []string{"x"}, EnumValues: []int64{1}, ArrayDims: []uint64{2},
			Base: &h5.Datatype{Class: h5.ClassEnum}},
		ds:       &h5sExtent{size: []uint64{2}, max: []uint64{3}},
		data:     []byte{1, 2},
		dtShared: &h5oShared{heapID: []byte{1}},
		dsShared: &h5oShared{heapID: []byte{2}},
		shared:   &h5oShared{heapID: []byte{3}},
	}, orig)
	assert.Nil(t, h5aCopy(nil))
}
