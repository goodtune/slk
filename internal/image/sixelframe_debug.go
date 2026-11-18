package image

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/gammons/slk/internal/debuglog"
)

// debugFrameWrite is a temporary diagnostic for the black thread images
// on Windows Terminal, not for upstream. It logs, under SLK_DEBUG, how
// each terminal write is laid out and every placement a frame paints.
func debugFrameWrite(p, sanitized []byte, marked, haveFrame bool, frame SixelFrame, plan SixelPlan, live int) {
	if !debuglog.Enabled() {
		return
	}
	if !marked {
		if live > 0 {
			debuglog.ImgRender("sixeldbg unmarked write len=%d head=%q", len(p), head(p, 60))
		}
		return
	}
	if !haveFrame || (plan.Painted == 0 && plan.Erased == 0) {
		return
	}
	debuglog.ImgRender("sixeldbg frame write len=%d syncStart=%v syncReset=%v placements=%d painted=%d erased=%d",
		len(p), bytes.Contains(sanitized, []byte("\x1b[?2026h")), bytes.Contains(sanitized, []byte("\x1b[?2026l")),
		len(frame.Placements), plan.Painted, plan.Erased)
	for _, pl := range frame.Placements {
		debuglog.ImgRender("sixeldbg  want row=%d col=%d rows=%d cols=%d bytes=%d head=%q key=%s",
			pl.Row, pl.Col, pl.Rows, pl.Cols, len(pl.Bytes), head(pl.Bytes, 24), shortKey(pl.Key))
	}
}

func head(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

func shortKey(k string) string {
	if i := strings.LastIndex(k, "/"); i > 12 {
		return k[:12] + "…" + k[i:]
	}
	return fmt.Sprint(k)
}
