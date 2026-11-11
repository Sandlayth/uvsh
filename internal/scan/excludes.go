package scan

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"
)

// Lines starting with '#' ignored. path.Match treats '\' as an escape, so
// `\#recycle` matches a name that starts with '#'.
func ReadExcludes(r io.Reader) ([]string, error) {
	var patterns []string
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), " \t\r") // trailing blanks never match intentionally
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := path.Match(line, ""); err != nil {
			return nil, fmt.Errorf("line %d: pattern %q: %w", n, line, err)
		}
		patterns = append(patterns, line)
	}
	return patterns, sc.Err()
}
