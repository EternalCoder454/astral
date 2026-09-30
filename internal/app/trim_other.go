//go:build !linux

package app

// trimHeap does nothing where the C allocator is not glibc's.
func trimHeap() {}
