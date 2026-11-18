package main

import (
	"io"
	"os"
)

// debugFrameDump is a temporary diagnostic, not for upstream: with
// SLK_DEBUG_FRAMES=<file> every byte slk writes to the terminal (frames
// and sixel images alike) is also appended to that file, so a session
// can be replayed in another terminal.
func debugFrameDump(out io.Writer) io.Writer {
	path := os.Getenv("SLK_DEBUG_FRAMES")
	if path == "" {
		return out
	}
	f, err := os.Create(path)
	if err != nil {
		return out
	}
	return io.MultiWriter(out, f)
}
