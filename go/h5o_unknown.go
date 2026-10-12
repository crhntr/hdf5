package hdf5

// Translated from src/H5Ounknown.c: the "unknown" message class
// (H5O_MSG_UNKNOWN) has no decode callback. Messages with a type ID this
// library does not know are given the h5oUnknownID type and their original
// type ID as native value (h5oUnknown) when the object header is decoded
// (h5oChunkDeserialize); the only callbacks of the C class free that value.
