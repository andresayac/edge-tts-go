// Package edgetts turns text into speech with the online text-to-speech
// service of Microsoft Edge, without Edge, Windows or an API key.
//
//	tts := edgetts.New()
//	if err := tts.Synthesize(ctx, "Hello, world!", "en-US-AriaNeural"); err != nil {
//		log.Fatal(err)
//	}
//	path, err := tts.ToFile("hello") // hello.mp3
//
// An EdgeTTS keeps the audio and the word boundaries of its last synthesis,
// so it is not safe for concurrent use: create one per goroutine.
package edgetts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var (
	// ErrNoAudio is returned when audio is asked for before a synthesis.
	ErrNoAudio = errors.New("edge-tts: no audio data available, did you call Synthesize first?")

	// ErrEmptyAudio is returned when the service answers without audio. It
	// does that when the voice cannot read the text: voices are locale
	// specific, so an English voice cannot read Chinese or Cyrillic text.
	ErrEmptyAudio = errors.New("edge-tts: the service returned no audio; " +
		"voices are locale specific, check that the voice matches the language of the text")

	// ErrNoMetadata is returned by SaveMetadata when there are no word boundaries.
	ErrNoMetadata = errors.New("edge-tts: no metadata available to save")
)

// HandshakeError is returned when the service refuses the WebSocket connection.
type HandshakeError struct {
	StatusCode int
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("edge-tts: the service refused the connection (HTTP %d)", e.StatusCode)
}

// WordBoundary tells when a word is spoken. Offset and Duration are in
// 100-nanosecond units, as the service sends them.
type WordBoundary struct {
	Type     string `json:"type"`
	Offset   int64  `json:"offset"`
	Duration int64  `json:"duration"`
	Text     string `json:"text"`
}

// Start returns when the word starts, from the beginning of the audio.
func (w WordBoundary) Start() time.Duration { return time.Duration(w.Offset) * 100 }

// End returns when the word ends, from the beginning of the audio.
func (w WordBoundary) End() time.Duration { return time.Duration(w.Offset+w.Duration) * 100 }

// AudioInfo describes the audio of the last synthesis.
type AudioInfo struct {
	Size              int
	Format            string
	EstimatedDuration time.Duration
}

// Option changes how a text is spoken.
type Option func(*settings)

type settings struct {
	pitch, rate, volume, format string
}

// WithPitch sets the pitch, from "-100Hz" to "+100Hz". A bare number is
// taken as hertz.
func WithPitch(pitch string) Option { return func(s *settings) { s.pitch = pitch } }

// WithRate sets the speaking rate, such as "+50%" or "-25%". A bare number
// is taken as a percentage.
func WithRate(rate string) Option { return func(s *settings) { s.rate = rate } }

// WithVolume sets the volume, from "-100%" to "+100%". A bare number is
// taken as a percentage.
func WithVolume(volume string) Option { return func(s *settings) { s.volume = volume } }

// WithOutputFormat sets the audio format, such as Audio24khz96kbitrateMonoMP3.
func WithOutputFormat(format string) Option { return func(s *settings) { s.format = format } }

// EdgeTTS synthesizes speech and keeps the result of its last synthesis.
type EdgeTTS struct {
	audio      []byte
	format     string
	boundaries []WordBoundary

	wssURL      string
	voicesURL   string
	idleTimeout time.Duration
	httpClient  *http.Client
	dialer      *websocket.Dialer
}

// New returns a client for the Edge text-to-speech service.
func New() *EdgeTTS {
	return &EdgeTTS{
		format:      Audio24khz48kbitrateMonoMP3,
		wssURL:      WSSURL,
		voicesURL:   VoicesURL,
		idleTimeout: 30 * time.Second,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 30 * time.Second,
		},
	}
}

// Synthesize speaks text with voice and keeps the audio and word
// boundaries, which Bytes, ToFile and WordBoundaries then return. An empty
// voice means DefaultVoice.
func (t *EdgeTTS) Synthesize(ctx context.Context, text, voice string, opts ...Option) error {
	return t.run(ctx, text, voice, opts, nil)
}

// Stream speaks text with voice and yields the audio as it arrives:
//
//	for chunk, err := range tts.Stream(ctx, text, voice) {
//		if err != nil {
//			return err
//		}
//		w.Write(chunk)
//	}
//
// Breaking out of the loop closes the connection. The audio is also kept,
// as with Synthesize.
func (t *EdgeTTS) Stream(ctx context.Context, text, voice string, opts ...Option) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		stopped := false
		err := t.run(ctx, text, voice, opts, func(chunk []byte) bool {
			stopped = !yield(chunk, nil)
			return !stopped
		})
		if err != nil && !stopped {
			yield(nil, err)
		}
	}
}

func (t *EdgeTTS) run(ctx context.Context, text, voice string, opts []Option, onAudio func([]byte) bool) error {
	s := settings{format: Audio24khz48kbitrateMonoMP3}
	for _, opt := range opts {
		opt(&s)
	}
	pitch, err := formatPitch(s.pitch)
	if err != nil {
		return err
	}
	rate, err := formatRate(s.rate)
	if err != nil {
		return err
	}
	volume, err := formatVolume(s.volume)
	if err != nil {
		return err
	}
	if voice == "" {
		voice = DefaultVoice
	}

	t.audio, t.boundaries, t.format = nil, nil, s.format

	conn, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	messages := []string{
		speechConfigMessage(s.format),
		ssmlMessage(randomHex(), buildSSML(text, voice, pitch, rate, volume)),
	}
	for _, msg := range messages {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			return t.readError(ctx, err)
		}
	}

	for {
		_ = conn.SetReadDeadline(time.Now().Add(t.idleTimeout))
		kind, data, err := conn.ReadMessage()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			return t.finish()
		}
		if err != nil {
			return t.readError(ctx, err)
		}

		switch kind {
		case websocket.TextMessage:
			path, body := parseTextFrame(data)
			switch path {
			case "audio.metadata":
				t.boundaries = append(t.boundaries, parseWordBoundaries(body)...)
			case "turn.end":
				return t.finish()
			}
		case websocket.BinaryMessage:
			path, body, err := parseBinaryFrame(data)
			if err != nil {
				return err
			}
			if path != "audio" || len(body) == 0 {
				continue
			}
			t.audio = append(t.audio, body...)
			if onAudio != nil && !onAudio(body) {
				return nil
			}
		}
	}
}

func (t *EdgeTTS) finish() error {
	if len(t.audio) == 0 {
		return ErrEmptyAudio
	}
	return nil
}

func (t *EdgeTTS) readError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("edge-tts: no response from the service for %s", t.idleTimeout)
	}
	return fmt.Errorf("edge-tts: %w", err)
}

func (t *EdgeTTS) connect(ctx context.Context) (*websocket.Conn, error) {
	for attempt := 0; ; attempt++ {
		url := t.wssURL +
			"?Ocp-Apim-Subscription-Key=" + TrustedClientToken +
			"&Sec-MS-GEC=" + secMSGEC(now()) +
			"&Sec-MS-GEC-Version=" + SecMSGECVersion +
			"&ConnectionId=" + randomHex()
		conn, resp, err := t.dialer.DialContext(ctx, url, wssHeaders())
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if resp == nil {
			return nil, fmt.Errorf("edge-tts: connecting to the service: %w", err)
		}
		// A refused signature usually means the local clock is off.
		if resp.StatusCode == http.StatusForbidden && attempt == 0 && adjustClock(resp.Header) {
			continue
		}
		return nil, &HandshakeError{StatusCode: resp.StatusCode}
	}
}

// Bytes returns the audio of the last synthesis.
func (t *EdgeTTS) Bytes() ([]byte, error) {
	if len(t.audio) == 0 {
		return nil, ErrNoAudio
	}
	return t.audio, nil
}

// Base64 returns the audio of the last synthesis encoded in base64.
func (t *EdgeTTS) Base64() (string, error) {
	audio, err := t.Bytes()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(audio), nil
}

var kbitratePattern = regexp.MustCompile(`(\d+)kbitrate`)

// Duration estimates the length of the audio from its size and bitrate.
// MP3 is constant bitrate; formats without one (opus) assume 48 kbit/s.
func (t *EdgeTTS) Duration() (time.Duration, error) {
	audio, err := t.Bytes()
	if err != nil {
		return 0, err
	}
	kbps := int64(48)
	if m := kbitratePattern.FindStringSubmatch(t.format); m != nil {
		if v, _ := strconv.ParseInt(m[1], 10, 64); v > 0 {
			kbps = v
		}
	}
	return time.Duration(int64(len(audio)) * 8 * int64(time.Millisecond) / kbps), nil
}

// Extension returns the file extension of the output format, such as "mp3".
func (t *EdgeTTS) Extension() string {
	f := t.format
	switch {
	case strings.Contains(f, "mp3"):
		return "mp3"
	case strings.Contains(f, "opus") && strings.Contains(f, "webm"):
		return "webm"
	case strings.Contains(f, "opus") && strings.Contains(f, "ogg"):
		return "ogg"
	case strings.Contains(f, "wav") || strings.Contains(f, "riff"):
		return "wav"
	case strings.Contains(f, "pcm") && strings.Contains(f, "raw"):
		return "pcm"
	case strings.Contains(f, "alaw"):
		return "alaw"
	case strings.Contains(f, "mulaw"):
		return "mulaw"
	case strings.Contains(f, "truesilk"):
		return "silk"
	case strings.Contains(f, "g722"):
		return "g722"
	case strings.Contains(f, "amr"):
		return "amr"
	}
	return "audio"
}

// AudioInfo returns the size, format and estimated duration of the audio.
func (t *EdgeTTS) AudioInfo() (AudioInfo, error) {
	duration, err := t.Duration()
	if err != nil {
		return AudioInfo{}, err
	}
	return AudioInfo{Size: len(t.audio), Format: t.Extension(), EstimatedDuration: duration}, nil
}

// ToFile writes the audio to basePath plus the extension of the output
// format, and returns the path it wrote.
func (t *EdgeTTS) ToFile(basePath string) (string, error) {
	audio, err := t.Bytes()
	if err != nil {
		return "", err
	}
	path := basePath + "." + t.Extension()
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// WordBoundaries returns when each word of the last synthesis is spoken.
func (t *EdgeTTS) WordBoundaries() []WordBoundary {
	return t.boundaries
}

// SaveMetadata writes the word boundaries to path as JSON.
func (t *EdgeTTS) SaveMetadata(path string) error {
	if len(t.boundaries) == 0 {
		return ErrNoMetadata
	}
	data, err := json.MarshalIndent(t.boundaries, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
