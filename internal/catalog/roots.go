package catalog

import (
	"fmt"
	"path/filepath"
	"strings"
)

// CheckRoot enforces that every file belongs to exactly one root: root may
// not be inside, nor contain, another registered root. Backends call it in
// the same transaction as the insert, so concurrent scans can't both pass.
func CheckRoot(root string, registered []string) error {
	for _, r := range registered {
		if r != root && (within(root, r) || within(r, root)) {
			return fmt.Errorf("%w %s", ErrRootOverlap, r)
		}
	}
	return nil
}

// within reports whether p is strictly below dir, both clean absolute paths.
func within(p, dir string) bool {
	sep := string(filepath.Separator)
	return p != dir && strings.HasPrefix(p, strings.TrimSuffix(dir, sep)+sep)
}
