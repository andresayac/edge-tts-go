package edgetts

import (
	"fmt"
	"strings"
)

// SRT writes boundaries as SRT subtitles, one cue per boundary.
func SRT(cues []Boundary) string {
	blocks := make([]string, len(cues))
	for i, c := range cues {
		blocks[i] = fmt.Sprintf("%d\n%s --> %s\n%s\n", i+1,
			subtitleTime(c.Offset, ","), subtitleTime(c.Offset+c.Duration, ","), c.Text)
	}
	return strings.Join(blocks, "\n")
}

// VTT writes boundaries as WebVTT subtitles, one cue per boundary.
func VTT(cues []Boundary) string {
	blocks := make([]string, len(cues))
	for i, c := range cues {
		blocks[i] = fmt.Sprintf("%s --> %s\n%s\n",
			subtitleTime(c.Offset, "."), subtitleTime(c.Offset+c.Duration, "."), c.Text)
	}
	return "WEBVTT\n\n" + strings.Join(blocks, "\n")
}

// subtitleTime formats 100-nanosecond ticks as hh:mm:ss plus milliseconds.
func subtitleTime(ticks int64, separator string) string {
	ms := (ticks + 5000) / 10000
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", ms/3600000, ms/60000%60, ms/1000%60, separator, ms%1000)
}
