package index

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

type LinkStatus int

const (
	LinkFile LinkStatus = iota + 1
	LinkDir
	LinkDangling
	LinkLoop
	LinkOutside // resolves outside the link's own root, maybe into another one
)

func (s LinkStatus) String() string {
	switch s {
	case LinkFile:
		return "file"
	case LinkDir:
		return "dir"
	case LinkDangling:
		return "dangling"
	case LinkLoop:
		return "loop"
	case LinkOutside:
		return "outside"
	}
	return "unknown"
}

// maxHops is Linux's MAXSYMLINKS: past 40 links, open fails with ELOOP.
const maxHops = 40

// ResolveLink reports what opening the link at path, holding target, would
// reach, judged from the catalog alone and only within the link's own root.
// Links in the target are followed as the kernel would, ".." included: after
// a link, ".." is the parent of where the link led, not of the link. All the
// lookups see one snapshot of the catalog.
func ResolveLink(ctx context.Context, cat catalog.Catalog, path, target string) (LinkStatus, error) {
	var s LinkStatus
	err := cat.Read(ctx, func(l catalog.Lookup) error {
		var err error
		s, err = resolveLink(ctx, l, filepath.Clean(path), target)
		return err
	})
	return s, err
}

func resolveLink(ctx context.Context, l catalog.Lookup, path, target string) (LinkStatus, error) {
	root, rootNode, err := catalog.FindRoot(ctx, l, filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	sep := string(filepath.Separator)
	queue := splitPath(target)
	if !filepath.IsAbs(target) {
		queue = append(splitPath(filepath.Dir(path)), queue...)
	}
	// cur is the real path reached so far. Below root, stack holds its nodes
	// from rootNode down; above root (an ancestor of it), stack is empty.
	var cur string
	var stack []catalog.Node
	fromTop := func() {
		cur, stack = sep, nil
		if root == sep {
			stack = []catalog.Node{rootNode}
		}
	}
	fromTop()
	for hops := 1; len(queue) > 0; { // the link at path is the first hop
		name := queue[0]
		queue = queue[1:]
		switch {
		case name == ".":
			continue
		case name == ".." && cur == sep:
			continue
		case name == "..":
			cur = filepath.Dir(cur)
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		case len(stack) == 0:
			// Above root, only the way down to it is visible.
			cur = filepath.Join(cur, name)
			if cur == root {
				stack = []catalog.Node{rootNode}
			} else if !strings.HasPrefix(root, strings.TrimSuffix(cur, sep)+sep) {
				return LinkOutside, nil
			}
			continue
		}
		dir := stack[len(stack)-1]
		if dir.Kind != uvsh.Dir {
			return LinkDangling, nil // ENOTDIR
		}
		n, ok, err := l.Child(ctx, dir, name)
		if err != nil {
			return 0, err
		} else if !ok {
			return LinkDangling, nil
		}
		if n.Kind != uvsh.Symlink {
			cur = filepath.Join(cur, name)
			stack = append(stack, n)
			continue
		}
		if hops++; hops > maxHops {
			return LinkLoop, nil
		}
		queue = append(splitPath(n.Target), queue...)
		if filepath.IsAbs(n.Target) {
			fromTop()
		}
	}
	switch {
	case len(stack) == 0:
		return LinkOutside, nil // an ancestor of root
	case stack[len(stack)-1].Kind == uvsh.Dir:
		return LinkDir, nil
	}
	return LinkFile, nil
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == filepath.Separator })
}
