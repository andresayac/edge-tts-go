package edgetts

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Seconds between the Windows epoch (1601-01-01) and the Unix epoch.
const windowsEpochOffset = 11644473600

// clockSkew is how far the local clock is behind the service, in
// nanoseconds. The signature only holds for a five minute window, so a
// machine with a wrong clock is refused until this is corrected from the
// Date header of the refusal. It is shared by every client.
var clockSkew atomic.Int64

func now() time.Time {
	return time.Now().Add(time.Duration(clockSkew.Load()))
}

// adjustClock sets the skew from the Date header of a response and reports
// whether the header could be read.
func adjustClock(h http.Header) bool {
	server, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		return false
	}
	clockSkew.Store(int64(time.Until(server)))
	return true
}

// secMSGEC returns the Sec-MS-GEC token: a SHA-256 of the time, in Windows
// file time rounded down to five minutes, followed by the client token.
func secMSGEC(t time.Time) string {
	ticks := t.Unix() + windowsEpochOffset
	ticks -= ticks % 300
	sum := sha256.Sum256([]byte(strconv.FormatInt(ticks*10_000_000, 10) + TrustedClientToken))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func randomHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func token32() string { return strings.ToUpper(randomHex()) }

func timestamp() string {
	return now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)")
}

var (
	pitchPattern  = regexp.MustCompile(`^[+-]?\d{1,3}(\.\d+)?Hz$`)
	numberPattern = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)
)

func signed(v float64, unit string) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if v >= 0 {
		s = "+" + s
	}
	return s + unit
}

// formatPitch accepts "+10Hz", "-5.5Hz" or a bare number of hertz.
func formatPitch(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "+0Hz", nil
	}
	if numberPattern.MatchString(s) {
		v, _ := strconv.ParseFloat(s, 64)
		return signed(v, "Hz"), nil
	}
	if !pitchPattern.MatchString(s) {
		return "", fmt.Errorf("edge-tts: invalid pitch %q: expected a value from \"-100Hz\" to \"+100Hz\" or a number", s)
	}
	return s, nil
}

// formatRate accepts "+10%", "-25%" or a bare number of percent.
func formatRate(s string) (string, error) {
	v, err := percent(s)
	if err != nil {
		return "", fmt.Errorf("edge-tts: invalid rate %q: expected a percentage such as \"+10%%\" or \"-25%%\"", s)
	}
	return signed(v, "%"), nil
}

// formatVolume accepts "+10%", "-25%" or a bare number from -100 to 100.
func formatVolume(s string) (string, error) {
	v, err := percent(s)
	if err != nil || v < -100 || v > 100 {
		return "", fmt.Errorf("edge-tts: invalid volume %q: expected a value from -100%% to +100%%", s)
	}
	return signed(v, "%"), nil
}

func percent(s string) (float64, error) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	if s == "" {
		return 0, nil
	}
	if !numberPattern.MatchString(s) {
		return 0, errors.New("not a number")
	}
	return strconv.ParseFloat(s, 64)
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

// cleanText drops what XML cannot carry; the service closes the connection
// without audio when the SSML holds a control character.
func cleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return ' '
		}
		return r
	}, s)
}

func buildSSML(text, voice, pitch, rate, volume string) string {
	return `<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="https://www.w3.org/2001/mstts" xml:lang="en-US">` +
		`<voice name="` + xmlEscaper.Replace(voice) + `">` +
		`<prosody pitch="` + pitch + `" rate="` + rate + `" volume="` + volume + `">` +
		xmlEscaper.Replace(cleanText(text)) +
		`</prosody></voice></speak>`
}

func speechConfigMessage(format string) string {
	quoted, _ := json.Marshal(format)
	return "X-Timestamp:" + timestamp() + "\r\n" +
		"Content-Type:application/json; charset=utf-8\r\n" +
		"Path:speech.config\r\n\r\n" +
		`{"context":{"synthesis":{"audio":{"metadataoptions":{"sentenceBoundaryEnabled":true,"wordBoundaryEnabled":true},"outputFormat":` +
		string(quoted) + `}}}}`
}

func ssmlMessage(requestID, ssml string) string {
	return "X-RequestId:" + requestID + "\r\n" +
		"Content-Type:application/ssml+xml\r\n" +
		"X-Timestamp:" + timestamp() + "\r\n" +
		"Path:ssml\r\n\r\n" + ssml
}

// parseTextFrame splits a text frame into its Path header and its body.
func parseTextFrame(data []byte) (path string, body []byte) {
	head, body, _ := bytes.Cut(data, []byte("\r\n\r\n"))
	return headerPath(head), body
}

// parseBinaryFrame splits a binary frame, which starts with the length of
// its headers as a big-endian uint16, into its Path header and its payload.
func parseBinaryFrame(data []byte) (path string, body []byte, err error) {
	if len(data) < 2 {
		return "", nil, errors.New("edge-tts: binary frame too short")
	}
	n := int(binary.BigEndian.Uint16(data))
	if len(data) < 2+n {
		return "", nil, errors.New("edge-tts: binary frame headers longer than the frame")
	}
	return headerPath(data[2 : 2+n]), data[2+n:], nil
}

func headerPath(head []byte) string {
	for _, line := range strings.Split(string(head), "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && k == "Path" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

type metadataMessage struct {
	Metadata []struct {
		Type string `json:"Type"`
		Data struct {
			Offset   int64 `json:"Offset"`
			Duration int64 `json:"Duration"`
			Text     struct {
				Text string `json:"Text"`
			} `json:"text"`
		} `json:"Data"`
	} `json:"Metadata"`
}

// parseBoundaries returns the word and sentence boundaries of an
// audio.metadata message.
func parseBoundaries(body []byte) []Boundary {
	var msg metadataMessage
	if json.Unmarshal(body, &msg) != nil {
		return nil
	}
	var out []Boundary
	for _, m := range msg.Metadata {
		if m.Type == "WordBoundary" || m.Type == "SentenceBoundary" {
			out = append(out, Boundary{
				Type:     m.Type,
				Offset:   m.Data.Offset,
				Duration: m.Data.Duration,
				Text:     m.Data.Text.Text,
			})
		}
	}
	return out
}

// Punctuation that opens a sentence and that the service leaves out of the
// text of its sentence boundaries.
var openingPunctuation = []string{"¿", "¡", "«", `"`, "'", "“", "‘", "(", "["}

// sentenceTextRestorer returns a function that puts back the opening
// punctuation the service drops from sentence boundaries, such as the ¿ of
// "¿Cómo estás?". It takes the sentences in order and finds each one in
// source.
func sentenceTextRestorer(source string) func(string) string {
	cursor := 0
	return func(text string) string {
		if text == "" {
			return text
		}
		i := strings.Index(source[cursor:], text)
		if i < 0 {
			return text
		}
		index := cursor + i
		start := index
		for moved := true; moved; {
			moved = false
			for _, mark := range openingPunctuation {
				if start-len(mark) >= cursor && strings.HasSuffix(source[:start], mark) {
					start -= len(mark)
					moved = true
				}
			}
		}
		cursor = index + len(text)
		return source[start:cursor]
	}
}

// filePath returns path plus "."+ext, unless path already ends with it.
func filePath(path, ext string) string {
	suffix := "." + ext
	if len(path) >= len(suffix) && strings.EqualFold(path[len(path)-len(suffix):], suffix) {
		return path
	}
	return path + suffix
}

// extension returns the file extension of an output format, such as "mp3".
func extension(format string) string {
	has := func(s string) bool { return strings.Contains(format, s) }
	switch {
	case has("mp3"):
		return "mp3"
	case has("opus") && has("webm"):
		return "webm"
	case has("opus") && has("ogg"):
		return "ogg"
	case has("wav") || has("riff"):
		return "wav"
	case has("pcm") && has("raw"):
		return "pcm"
	case has("alaw"):
		return "alaw"
	case has("mulaw"):
		return "mulaw"
	case has("truesilk"):
		return "silk"
	case has("g722"):
		return "g722"
	case has("amr"):
		return "amr"
	}
	return "audio"
}

var kbitratePattern = regexp.MustCompile(`(\d+)kbitrate`)

// estimateDuration estimates the length of size bytes of audio from the
// bitrate of the format. MP3 is constant bitrate; formats without one
// (opus) assume 48 kbit/s.
func estimateDuration(size int, format string) time.Duration {
	kbps := int64(48)
	if m := kbitratePattern.FindStringSubmatch(format); m != nil {
		if v, _ := strconv.ParseInt(m[1], 10, 64); v > 0 {
			kbps = v
		}
	}
	return time.Duration(int64(size) * 8 * int64(time.Millisecond) / kbps)
}
