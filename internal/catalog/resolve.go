package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sandlayth/uvsh/internal/uvsh"
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
	uvsh.Entry
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

	root, n, err := FindRoot(ctx, l, path)
	if err != nil {
		return Node{}, err
	}
	n.Path = filepath.Base(root)

	rel := strings.TrimPrefix(strings.TrimPrefix(path, root), string(filepath.Separator))
	if rel == "" {
		return n, nil
	}
	for name := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if n.Kind != uvsh.Dir {
			return Node{}, notFound // links are never followed
		}
		var ok bool
		if n, ok, err = l.Child(ctx, n, name); err != nil {
			return Node{}, err
		} else if !ok {
			return Node{}, notFound
		}
	}
	return n, nil
}

// FindRoot returns the most specific registered root containing the clean,
// absolute path, and the root's top dir. It tries path, then each parent:
// one lookup per level.
func FindRoot(ctx context.Context, l Lookup, path string) (string, Node, error) {
	for root := path; ; {
		n, ok, err := l.Root(ctx, root)
		if err != nil || ok {
			return root, n, err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", Node{}, fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		root = parent
	}
}
