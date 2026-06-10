package component

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
	artifact "github.com/tardigradeproj/heir/artifacts"
)

// embedPath maps a caller-supplied path of the form "worker/foo" to the
// arch-specific embedded path "worker/<goarch>/foo".
func embedPath(src string) string {
	tail := strings.TrimPrefix(src, "worker/")
	return fmt.Sprintf("worker/%s/%s", runtime.GOARCH, tail)
}

func extractStreamed(src string, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	source, err := artifact.FS.Open(embedPath(src) + ".zst")
	if err != nil {
		return err
	}
	defer source.Close()

	dec, err := zstd.NewReader(source)
	if err != nil {
		return err
	}
	defer dec.Close()

	return copyToFile(dec, dst)
}

func copyToFile(r io.Reader, dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func extractTarZstFrom(fsys fs.FS, src string, dstDir string) error {
	source, err := fsys.Open(embedPath(src))
	if err != nil {
		return err
	}
	defer source.Close()

	dec, err := zstd.NewReader(source)
	if err != nil {
		return err
	}
	defer dec.Close()

	tr := tar.NewReader(dec)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		dst := filepath.Join(dstDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := copyToFile(tr, dst); err != nil {
			return fmt.Errorf("extracting %s: %w", name, err)
		}
	}
	return nil
}
