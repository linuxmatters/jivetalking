package processor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSiblingStatsPath(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "presenter.wav")

	statsPath, err := createSiblingStatsPath(targetPath, "loudnorm")
	if err != nil {
		t.Fatalf("createSiblingStatsPath() failed: %v", err)
	}
	defer os.Remove(statsPath)

	if filepath.Dir(statsPath) != dir {
		t.Errorf("stats dir = %q, want %q", filepath.Dir(statsPath), dir)
	}

	base := filepath.Base(statsPath)
	if !strings.HasPrefix(base, ".loudnorm-") {
		t.Errorf("stats basename = %q, want .loudnorm- prefix", base)
	}
	if !strings.HasSuffix(statsPath, ".tmp.json") {
		t.Errorf("stats path = %q, want .tmp.json suffix", statsPath)
	}

	info, err := os.Stat(statsPath)
	if err != nil {
		t.Fatalf("stats path %q was not reserved: %v", statsPath, err)
	}
	if info.Size() != 0 {
		t.Errorf("stats path %q size = %d, want 0", statsPath, info.Size())
	}
}

func TestCreateSiblingStatsPathRejectsSeparatorMarker(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "presenter.wav")

	if _, err := createSiblingStatsPath(targetPath, "a/b"); err == nil {
		t.Fatal("createSiblingStatsPath() with separator marker = nil error, want error")
	}
}
