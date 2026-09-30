//go:build linux

package app

// #include <malloc.h>
import "C"

// trimHeap hands memory the C allocator is holding, freed but kept, back to
// the system. GTK frees a closed chat's widgets through glibc's malloc, which
// keeps what it freed in its arenas for the next allocation, so resident
// memory stayed high after a long session of switching chats although
// nothing was leaking. malloc_trim returns the free pages at the top of each
// arena and in the middle of them. See scheduleTidy.
func trimHeap() { C.malloc_trim(0) }
