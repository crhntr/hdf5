package hdf5

// Tests for errors.go and file.go written from the mutants gremlins left
// alive.

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMutationErrorIsMessage: an *Error target with a message matches
// errors with that message only (errors.go:143).
func TestMutationErrorIsMessage(t *testing.T) {
	err := wrapf(errorf(H5E_BTREE, H5E_NOTFOUND, "inner"), H5E_SYM, H5E_CANTGET, "outer")
	assert.ErrorIs(t, err, &Error{Msg: "outer"})
	assert.ErrorIs(t, err, &Error{Minor: H5E_NOTFOUND, Msg: "inner"})
	assert.False(t, errors.Is(err, &Error{Msg: "other"}))
	assert.False(t, errors.Is(err, &Error{Major: H5E_SYM, Msg: "inner"}))
	assert.ErrorIs(t, err, ErrNotFound)
}
