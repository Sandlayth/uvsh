// Package uvsh holds the vocabulary every other package shares: the types,
// with no behaviour and no dependencies beyond the standard library.
package uvsh

import "time"

// Kind values are stored as is, so they must never change.
type Kind string

const (
	Dir     Kind = "dir"
	File    Kind = "file"
	Symlink Kind = "link"
	Other   Kind = "other" // fifos, sockets, devices: walked, never stored
)

// Entry is a filesystem entry, as a walk finds it and as a catalog records it.
type Entry struct {
	Path    string // slash-separated, relative to the walked root or listed dir
	Kind    Kind
	Size    int64 // files only
	ModTime time.Time
	Target  string // links only, as stored in the link
}
