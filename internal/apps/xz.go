package apps

import (
	"bufio"
	"io"

	"github.com/ulikunitz/xz"
)

// Jellyfin and jellyfin-ffmpeg publish .tar.xz archives (for macOS and
// Linux), which the standard library cannot read.
func init() {
	decompressors[".tar.xz"] = func(r io.Reader) (io.Reader, error) {
		return xz.NewReader(bufio.NewReaderSize(r, 1<<20))
	}
}
