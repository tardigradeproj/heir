package component

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

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

	// Open the embedded file as a stream
	source, err := artifact.FS.Open(embedPath(src))
	if err != nil {
		return err
	}
	defer source.Close()

	// Create the destination file
	dest, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dest, source); err != nil {
		dest.Close()
		return err
	}
	return dest.Close()
}
