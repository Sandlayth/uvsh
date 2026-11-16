package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Lookup is what path resolution needs from a backend. Backends bind it to
// one read transaction, so a whole resolution sees a single snapshot.
type Lookup interface {
	// Root returns the root dir recorded for the root registered at exactly path.
	Root(ctx context.Context, path string) (Node, bool, error)
	// Child returns the entry named name directly in dir.
	Child(ctx context.Context, dir Node, name string) (Node, bool, error)
}

// Node is an Entry plus the backend's handle for it.
type Node struct {
	Entry
	ID int64
}

// Resolve finds the entry at an absolute path, under the most specific
// registered root that contains it.
func Resolve(ctx context.Context, l Lookup, path string) (Node, error) {
	if !filepath.IsAbs(path) {
		return Node{}, fmt.Errorf("path %q: must be absolute", path)
	}
	path = filepath.Clean(path)
	notFound := fmt.Errorf("%s: %w", path, ErrNotFound)

	// Try path, then each parent: one lookup per level.
	var n Node
	root := path
	for {
		var ok bool
		var err error
		if n, ok, err = l.Root(ctx, root); err != nil {
			return Node{}, err
		} else if ok {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return Node{}, notFound
		}
		root = parent
	}
	n.Name = filepath.Base(root)

	rel := strings.TrimPrefix(strings.TrimPrefix(path, root), string(filepath.Separator))
	if rel == "" {
		return n, nil
	}
	for name := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if n.Kind != Dir {
			return Node{}, notFound // a file can't have children
		}
		var ok bool
		var err error
		if n, ok, err = l.Child(ctx, n, name); err != nil {
			return Node{}, err
		} else if !ok {
			return Node{}, notFound
		}
	}
	return n, nil
}
