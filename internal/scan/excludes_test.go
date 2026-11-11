package scan

import (
	"slices"
	"strings"
	"testing"
)

func TestReadExcludes(t *testing.T) {
	in := "# comment\n\n.thumbnails\r\n*.tmp  \n\\#trash\n   \nbackups/old/*\n"
	got, err := ReadExcludes(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".thumbnails", "*.tmp", `\#trash`, "backups/old/*"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReadExcludesBadPattern(t *testing.T) {
	_, err := ReadExcludes(strings.NewReader("ok\n[unclosed\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want an error naming line 2", err)
	}
}
