package db

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestEmbeddedMigrationsParse(t *testing.T) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("iofs.New() error = %v", err)
	}
	defer src.Close()
}

func TestEmbeddedMigrationsAreForwardOnly(t *testing.T) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	var up, down int
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".up.sql"):
			up++
		case strings.HasSuffix(e.Name(), ".down.sql"):
			down++
		}
	}

	if up < 2 {
		t.Errorf("found %d .up.sql migrations, want at least 2", up)
	}
	if down != 0 {
		t.Errorf("found %d .down.sql migrations, want 0 (migrations are forward-only)", down)
	}
}
