package sqlite

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrations(t *testing.T) {
	m, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	if len(m) != len(migrations) {
		t.Errorf("loaded %d migrations, package has %d", len(m), len(migrations))
	}
}

func TestLoadMigrations(t *testing.T) {
	sql := &fstest.MapFile{Data: []byte("SELECT 1;")}
	tests := []struct {
		name  string
		files []string
		want  int // number of migrations; 0 means an error is expected
	}{
		{"in order", []string{"0001_init.sql", "0002_more.sql"}, 2},
		{"gap", []string{"0001_init.sql", "0003_skip.sql"}, 0},
		{"duplicate version", []string{"0001_a.sql", "0001_b.sql"}, 0},
		{"not starting at 1", []string{"0002_init.sql"}, 0},
		{"unpadded number", []string{"1_init.sql"}, 0},
		{"no name", []string{"0001.sql"}, 0},
		{"empty name", []string{"0001_.sql"}, 0},
		{"wrong extension", []string{"0001_init.txt"}, 0},
		{"none", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{"migrations": &fstest.MapFile{Mode: fs.ModeDir | 0o755}} // dir, even if empty
			for _, f := range tt.files {
				fsys["migrations/"+f] = sql
			}
			got, err := loadMigrations(fsys)
			switch {
			case tt.want == 0 && err == nil:
				t.Errorf("loaded %d migrations, want an error", len(got))
			case tt.want > 0 && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.want > 0 && len(got) != tt.want:
				t.Errorf("loaded %d migrations, want %d", len(got), tt.want)
			}
		})
	}
}
