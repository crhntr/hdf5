package hdf5

// Translated from src/H5T.c (the parts used by the read paths).

import (
	"github.com/crhntr/hdf5/go/h5"
)

// h5tDetectClass is H5T_detect_class with from_api = false: whether dt is
// of class cls or contains a member, element or base type of that class.
// Variable-length strings are of the H5T_VLEN class here (the library's
// internal view).
func h5tDetectClass(dt *h5.Datatype, cls h5.Class) bool {
	if dt == nil {
		return false
	}

	/* Check if this type is the correct type */
	if dt.Class == cls {
		return true
	}

	/* check for types that might have the correct type as a component */
	switch dt.Class {
	case h5.ClassCompound:
		for _, m := range dt.Members {
			/* Check if this field's type is the correct type */
			if m.Type != nil && m.Type.Class == cls {
				return true
			}

			/* Recurse if it's VL, compound, enum or array */
			if h5tDetectClass(m.Type, cls) {
				return true
			}
		}
	case h5.ClassArray, h5.ClassVlen, h5.ClassEnum:
		return h5tDetectClass(dt.Base, cls)
	}
	return false
}
