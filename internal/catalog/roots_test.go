package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckRoot(t *testing.T) {
	for _, tt := range []struct {
		name       string
		registered []string
		root       string
		want       string // the overlapping root, or "" if root may be added
	}{
		{"inside a root", []string{"/data"}, "/data/photos", "/data"},
		{"deep inside a root", []string{"/data"}, "/data/photos/2023", "/data"},
		{"contains a root", []string{"/data/photos"}, "/data", "/data/photos"},
		{"contains a deep root", []string{"/data/photos/2023"}, "/data", "/data/photos/2023"},
		{"slash contains everything", []string{"/data"}, "/", "/data"},
		{"everything is inside slash", []string{"/"}, "/data", "/"},
		{"shared name prefix", []string{"/data"}, "/database", ""},
		{"shared name prefix, other way", []string{"/database", "/data0", "/data.d"}, "/data", ""},
		{"siblings", []string{"/data/photos"}, "/data/videos", ""},
		{"same root again", []string{"/data"}, "/data", ""},
		{"nothing registered", nil, "/data", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRoot(tt.root, tt.registered)
			if tt.want == "" {
				if err != nil {
					t.Errorf("CheckRoot(%s) = %v, want nil", tt.root, err)
				}
				return
			}
			if !errors.Is(err, ErrRootOverlap) || !strings.HasSuffix(err.Error(), " "+tt.want) {
				t.Errorf("CheckRoot(%s) = %v, want ErrRootOverlap naming %s", tt.root, err, tt.want)
			}
		})
	}
}
