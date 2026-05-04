package processor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrOutputExists is returned when a publish destination already exists.
var ErrOutputExists = errors.New("output already exists")

var (
	processorLink   = os.Link
	processorRemove = os.Remove
)

// DestinationExistsError identifies the output path that blocked a no-clobber publish.
type DestinationExistsError struct {
	Path string
}

func (e *DestinationExistsError) Error() string {
	return fmt.Sprintf("output already exists: %s", e.Path)
}

func (e *DestinationExistsError) Unwrap() error {
	return ErrOutputExists
}

// createSiblingTempPath creates a hidden, same-directory temp path whose basename
// includes the marker and ends in .tmp.flac; tests pin the exact naming pattern.
func createSiblingTempPath(targetPath, marker string) (string, error) {
	if marker == "" || filepath.Base(marker) != marker {
		return "", fmt.Errorf("invalid temp marker: %q", marker)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), "."+marker+"-*.tmp.flac")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary output next to %s: %w", targetPath, err)
	}

	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("failed to close temporary output %s: %w", tempPath, err)
	}

	return tempPath, nil
}

// renameNoClobber publishes a same-directory temp file by linking src to dst,
// then removing src. The link provides atomic no-clobber behaviour and avoids a
// check-then-use destination reservation that os.Rename could later overwrite.
func renameNoClobber(src, dst string) error {
	if err := processorLink(src, dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return &DestinationExistsError{Path: dst}
		}
		return fmt.Errorf("failed to publish output to %s: %w", dst, err)
	}

	if err := processorRemove(src); err != nil {
		return fmt.Errorf("published output to %s but failed to remove temporary source %s: %w", dst, src, err)
	}

	return nil
}
