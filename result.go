package edgetts

import "time"

// Result is what Generate returns: the audio of one synthesis with its
// boundaries, kept apart from the EdgeTTS.
type Result struct {
	Audio []byte
	// Format is the file extension of the output format, such as "mp3".
	Format string
	// OutputFormat is the format the service was asked for, such as
	// "audio-24khz-48kbitrate-mono-mp3".
	OutputFormat       string
	WordBoundaries     []WordBoundary
	SentenceBoundaries []SentenceBoundary
	// Duration is estimated from the size and the bitrate.
	Duration time.Duration
}

// ToFile writes the audio to path, adding the extension of the format
// unless path already ends with it, and returns the path it wrote.
func (r *Result) ToFile(path string) (string, error) {
	return writeAudio(path, r.Format, r.Audio)
}

// SRT returns subtitles, one cue per sentence.
func (r *Result) SRT() string { return SRT(cues(r.SentenceBoundaries, r.WordBoundaries)) }

// VTT returns WebVTT subtitles, one cue per sentence.
func (r *Result) VTT() string { return VTT(cues(r.SentenceBoundaries, r.WordBoundaries)) }
