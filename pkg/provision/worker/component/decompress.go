package component

import (
	"io"
	"os"

	artifact "github.com/tardigrade-runtime/samaritano/artifacts"
)

func extractStreamed(src string, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	// Open the embedded file as a stream
	source, err := artifact.FS.Open(src)
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
