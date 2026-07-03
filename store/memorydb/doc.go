// Package memorydb provides an in-memory implementation of the [store.Store]
// interface backed by a map with sync.RWMutex concurrency control.
//
// It is intended for development, testing, and unit tests where zero external
// dependencies are required.
package memorydb
