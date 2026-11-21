package browse

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/index"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

// put records root as holding a.txt, photos/2023/ and the given links, in
// walk order, without touching the disk.
func put(t *testing.T, c catalog.Catalog, root string, links map[string]string) {
	t.Helper()
	ctx := t.Context()
	w, err := c.BeginScan(ctx, root, catalog.ScanOptions{BatchSize: 1000})
	must(t, err)
	defer w.Abort()
	for _, dir := range []string{".", "photos", "photos/2023"} {
		must(t, w.Add(ctx, uvsh.Entry{Path: dir, Kind: uvsh.Dir}))
		if dir == "." {
			must(t, w.Add(ctx, uvsh.Entry{Path: "a.txt", Kind: uvsh.File}))
		}
		for name, target := range links {
			if path.Dir(name) == dir {
				must(t, w.Add(ctx, uvsh.Entry{Path: name, Kind: uvsh.Link, Target: target}))
			}
		}
	}
	_, err = w.Finish(ctx, true)
	must(t, err)
}

func TestResolveLink(t *testing.T) {
	c := open(t)
	want := map[string]LinkStatus{}
	links := map[string]string{}
	for _, l := range []struct {
		name, target string
		want         LinkStatus
	}{
		{"file", "a.txt", LinkFile},
		{"dir", "photos", LinkDir},
		{"abs", "/data/a.txt", LinkFile},
		{"self-root", ".", LinkDir},
		{"back-in", "../data/a.txt", LinkFile},
		{"above", "..", LinkOutside},
		{"sideways", "../etc/passwd", LinkOutside},
		{"other-root", "/other/a.txt", LinkOutside}, // reachable, but from /other only
		{"dangling", "nope", LinkDangling},
		{"through-file", "a.txt/x", LinkDangling},
		{"self", "self", LinkLoop},
		{"ping", "pong", LinkLoop},
		{"pong", "ping", LinkLoop},
		{"chain", "file", LinkFile},
		{"photos/up", "../a.txt", LinkFile},
		{"photos/2023/via", "../../dir/2023", LinkDir}, // through a link mid-path
		// ".." after a link is the parent of where it led: photos, which has
		// no a.txt. Cleaning the path as text would wrongly find /data/a.txt.
		{"p2023", "photos/2023", LinkDir},
		{"dotdot", "p2023/../a.txt", LinkDangling},
		// Linux follows at most 40 links: c1 takes 40 hops, c0 one too many.
		{"c0", "c1", LinkLoop},
	} {
		links[l.name], want[l.name] = l.target, l.want
	}
	for i := 1; i <= 40; i++ {
		links[fmt.Sprintf("c%d", i)], want[fmt.Sprintf("c%d", i)] = fmt.Sprintf("c%d", i+1), LinkFile
	}
	links["c40"] = "a.txt"
	put(t, c, "/data", links)
	put(t, c, "/other", nil)

	for name, w := range want {
		got, err := ResolveLink(t.Context(), c, "/data/"+name, links[name])
		if err != nil || got != w {
			t.Errorf("%s -> %s: %v, %v; want %v", name, links[name], got, err, w)
		}
	}
	if _, err := ResolveLink(t.Context(), c, "/elsewhere/l", "x"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("link outside every root: error = %v, want ErrNotFound", err)
	}
}

// At /, ".." stays at / and every absolute target is inside the root.

func TestResolveLinkRootIsSlash(t *testing.T) {
	c := open(t)
	links := map[string]string{"abs": "/a.txt", "photos/up": "../../../photos"}
	put(t, c, "/", links)
	for name, w := range map[string]LinkStatus{"abs": LinkFile, "photos/up": LinkDir} {
		if got, err := ResolveLink(t.Context(), c, "/"+name, links[name]); err != nil || got != w {
			t.Errorf("%s: %v, %v; want %v", name, got, err, w)
		}
	}
}

// The kernel is the reference: each link's status must match what stat(2)
// does with it, and outside means stat lands beyond the root.

func TestResolveLinkMatchesKernel(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	root := filepath.Join(tmp, "data")
	must(t, os.MkdirAll(filepath.Join(root, "photos/2023"), 0o755))
	must(t, os.MkdirAll(filepath.Join(tmp, "elsewhere"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "elsewhere/x"), nil, 0o644))
	links := map[string]string{
		"file": "a.txt", "dir": "photos", "abs": root + "/a.txt", "self-root": ".",
		"back-in": "../data/a.txt", "above": "..", "sideways": "../elsewhere/x", "abs-out": tmp + "/elsewhere",
		"dangling": "nope", "through-file": "a.txt/x", "self": "self", "ping": "pong", "pong": "ping",
		"p2023": "photos/2023", "dotdot": "p2023/../a.txt", "photos/up": "../a.txt",
		"photos/2023/via": "../../dir/2023", "c0": "c1",
	}
	for i := 1; i <= 40; i++ {
		links[fmt.Sprintf("c%d", i)] = fmt.Sprintf("c%d", i+1)
	}
	links["c40"] = "a.txt"
	for name, target := range links {
		must(t, os.Symlink(target, filepath.Join(root, name)))
	}
	c := open(t)
	_, err = index.Record(t.Context(), c, root, index.Options{BatchSize: 1000})
	must(t, err)

	for name, target := range links {
		p := filepath.Join(root, name)
		var want LinkStatus
		fi, err := os.Stat(p)
		switch {
		case errors.Is(err, syscall.ELOOP):
			want = LinkLoop
		case err != nil:
			want = LinkDangling
		default:
			real, err := filepath.EvalSymlinks(p)
			must(t, err)
			if real != root && !strings.HasPrefix(real, root+"/") {
				want = LinkOutside
			} else if fi.IsDir() {
				want = LinkDir
			} else {
				want = LinkFile
			}
		}
		if got, err := ResolveLink(t.Context(), c, p, target); err != nil || got != want {
			t.Errorf("%s -> %s: %v, %v; kernel says %v", name, target, got, err, want)
		}
	}
}
