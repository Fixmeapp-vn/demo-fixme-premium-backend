package migrations_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/user/migrations"
)

// TestEmbeddedMigrationsAreForwardOnlyAndContiguous parses the embedded SQL as
// a migrate source and asserts the versions start at 1, are contiguous, and
// carry up migrations only. No database is needed.
func TestEmbeddedMigrationsAreForwardOnlyAndContiguous(t *testing.T) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("iofs.New() error = %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	version, err := src.First()
	if err != nil {
		t.Fatalf("source has no migrations: %v", err)
	}

	var versions []uint
	for {
		versions = append(versions, version)

		if _, _, err := src.ReadUp(version); err != nil {
			t.Errorf("version %d has no up migration: %v", version, err)
		}
		if _, _, err := src.ReadDown(version); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("version %d has a down migration; migrations must be forward-only (err = %v)", version, err)
		}

		next, err := src.Next(version)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			t.Fatalf("Next(%d) error = %v", version, err)
		}
		version = next
	}

	want := []uint{1, 2}
	if len(versions) != len(want) {
		t.Fatalf("versions = %v, want %v", versions, want)
	}
	for i, v := range want {
		if versions[i] != v {
			t.Errorf("versions = %v, want contiguous %v", versions, want)
			break
		}
	}
}
