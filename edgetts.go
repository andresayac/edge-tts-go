// Package edgetts turns text into speech with the online text-to-speech
// service of Microsoft Edge, without Edge, Windows or an API key.
//
//	tts := edgetts.New()
//	if err := tts.Synthesize(ctx, "Hello, world!", "en-US-AriaNeural"); err != nil {
//		log.Fatal(err)
//	}
//	path, err := tts.ToFile("hello") // hello.mp3
//
// Synthesize and Stream keep the audio and the boundaries of the last
// synthesis in the EdgeTTS, so they are not safe for concurrent use.
// Generate returns them in a Result instead and, like Voices, can run from
// many goroutines at once.
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
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	// ErrNoAudio is returned when audio is asked for before a synthesis.
	ErrNoAudio = errors.New("edge-tts: no audio data available, did you call Synthesize first?")

	// ErrEmptyAudio is returned when the service answers without audio. It
	// does that when the voice cannot read the text: voices are locale
	// specific, so an English voice cannot read Chinese or Cyrillic text.
	// It does the same for a text of only punctuation.
	ErrEmptyAudio = errors.New("edge-tts: the service returned no audio; " +
		"voices are locale specific, check that the voice matches the language of the text " +
		"and that the text has words to read")

	// ErrEmptyText is returned, without connecting, for a text that is
	// empty or only whitespace.
	ErrEmptyText = errors.New("edge-tts: the text is empty")

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

// RefusedError is returned when the service closes the connection with a
// reason, as it does for an unsupported voice or output format.
type RefusedError struct {
	// Code is the WebSocket close code, such as 1007.
	Code int
	// Reason is what the service said, such as "Unsupported voice xx-XX-NobodyNeural."
	Reason string
}

func (e *RefusedError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("edge-tts: the service refused the request (close code %d)", e.Code)
	}
	return "edge-tts: the service refused the request: " + e.Reason
}

// Boundary tells when a word or a sentence is spoken. Offset and Duration
// are in 100-nanosecond units, as the service sends them.
type Boundary struct {
	// Type is "WordBoundary" or "SentenceBoundary".
	Type     string `json:"type"`
	Offset   int64  `json:"offset"`
	Duration int64  `json:"duration"`
	Text     string `json:"text"`
}

// WordBoundary tells when a word is spoken.
type WordBoundary = Boundary

// SentenceBoundary tells when a sentence is spoken.
type SentenceBoundary = Boundary

// Start returns when the word or sentence starts, from the beginning of the audio.
func (b Boundary) Start() time.Duration { return time.Duration(b.Offset) * 100 }

// End returns when the word or sentence ends, from the beginning of the audio.
func (b Boundary) End() time.Duration { return time.Duration(b.Offset+b.Duration) * 100 }

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

// ClientOption configures an EdgeTTS. The defaults come from the constants
// of the package, so when Microsoft changes the service the new values can
// be set without waiting for a release.
type ClientOption func(*EdgeTTS)

// WithWSSURL sets the WebSocket endpoint of the synthesis.
func WithWSSURL(url string) ClientOption { return func(t *EdgeTTS) { t.wssURL = url } }

// WithVoicesURL sets the URL of the voice list.
func WithVoicesURL(url string) ClientOption { return func(t *EdgeTTS) { t.voicesURL = url } }

// WithSecMSGECVersion sets the Sec-MS-GEC-Version sent with every request.
func WithSecMSGECVersion(version string) ClientOption {
	return func(t *EdgeTTS) { t.secMSGECVersion = version }
}

// WithHeader adds a header to every request, or replaces a default one
// such as User-Agent.
func WithHeader(name, value string) ClientOption {
	return func(t *EdgeTTS) { t.headers.Set(name, value) }
}

// WithTimeout sets how long to wait without data from the service before
// giving up. The default is 30 seconds. A context bounds a whole call.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(t *EdgeTTS) {
		t.idleTimeout = timeout
		t.httpClient.Timeout = timeout
		t.dialer.HandshakeTimeout = timeout
	}
}

// WithVoicesCacheTTL sets how long Voices reuses the voice list. The
// default is 10 minutes; 0 fetches it every time.
func WithVoicesCacheTTL(ttl time.Duration) ClientOption {
	return func(t *EdgeTTS) { t.voicesCacheTTL = ttl }
}

// EdgeTTS synthesizes speech and keeps the result of its last synthesis.
type EdgeTTS struct {
	audio     []byte
	format    string
	words     []Boundary
	sentences []Boundary

	wssURL          string
	voicesURL       string
	secMSGECVersion string
	headers         http.Header
	idleTimeout     time.Duration
	voicesCacheTTL  time.Duration
	httpClient      *http.Client
	dialer          *websocket.Dialer

	voicesMu      sync.Mutex
	voicesCache   []Voice
	voicesExpires time.Time
}

// New returns a client for the Edge text-to-speech service.
func New(opts ...ClientOption) *EdgeTTS {
	t := &EdgeTTS{
		format:          Audio24khz48kbitrateMonoMP3,
		wssURL:          WSSURL,
		voicesURL:       VoicesURL,
		secMSGECVersion: SecMSGECVersion,
		headers:         http.Header{},
		idleTimeout:     30 * time.Second,
		voicesCacheTTL:  10 * time.Minute,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Synthesize speaks text with voice and keeps the audio and the
// boundaries, which Bytes, ToFile, WordBoundaries and SRT then return. An
// empty voice means DefaultVoice.
func (t *EdgeTTS) Synthesize(ctx context.Context, text, voice string, opts ...Option) error {
	for _, err := range t.Stream(ctx, text, voice, opts...) {
		if err != nil {
			return err
		}
	}
	return nil
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
		t.audio, t.words, t.sentences = nil, nil, nil
		stopped := false
		format, err := t.session(ctx, text, voice, opts, func(chunk []byte) bool {
			t.audio = append(t.audio, chunk...)
			stopped = !yield(chunk, nil)
			return !stopped
		}, collector(text, &t.words, &t.sentences))
		if format != "" {
			t.format = format
		}
		if err != nil && !stopped {
			yield(nil, err)
		}
	}
}

// Generate speaks text with voice and returns the audio, the boundaries
// and the duration, without changing what the EdgeTTS keeps. It is safe to
// call from many goroutines at once.
func (t *EdgeTTS) Generate(ctx context.Context, text, voice string, opts ...Option) (*Result, error) {
	r := &Result{}
	format, err := t.session(ctx, text, voice, opts, func(chunk []byte) bool {
		r.Audio = append(r.Audio, chunk...)
		return true
	}, collector(text, &r.WordBoundaries, &r.SentenceBoundaries))
	if err != nil {
		return nil, err
	}
	r.OutputFormat = format
	r.Format = extension(format)
	r.Duration = estimateDuration(len(r.Audio), format)
	return r, nil
}

// collector returns a function that files each boundary in words or
// sentences, putting back the opening punctuation the service drops from
// sentences.
func collector(text string, words, sentences *[]Boundary) func(Boundary) {
	restore := sentenceTextRestorer(text)
	return func(b Boundary) {
		if b.Type == "WordBoundary" {
			*words = append(*words, b)
			return
		}
		b.Text = restore(b.Text)
		*sentences = append(*sentences, b)
	}
}

// session runs one synthesis. It reads the configuration of t but changes
// nothing in it, and returns the output format it asked for.
func (t *EdgeTTS) session(ctx context.Context, text, voice string, opts []Option,
	onAudio func([]byte) bool, onBoundary func(Boundary)) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", ErrEmptyText
	}
	s := settings{format: Audio24khz48kbitrateMonoMP3}
	for _, opt := range opts {
		opt(&s)
	}
	pitch, err := formatPitch(s.pitch)
	if err != nil {
		return "", err
	}
	rate, err := formatRate(s.rate)
	if err != nil {
		return "", err
	}
	volume, err := formatVolume(s.volume)
	if err != nil {
		return "", err
	}
	if voice == "" {
		voice = DefaultVoice
	}

	conn, err := t.connect(ctx)
	if err != nil {
		return s.format, err
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
			return s.format, t.readError(ctx, err)
		}
	}

	received := false
	finish := func() (string, error) {
		if !received {
			return s.format, ErrEmptyAudio
		}
		return s.format, nil
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(t.idleTimeout))
		kind, data, err := conn.ReadMessage()
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) && ctx.Err() == nil {
			switch closeErr.Code {
			case websocket.CloseNormalClosure:
				return finish()
			case websocket.CloseAbnormalClosure:
				// The connection dropped without a close frame.
				return s.format, errors.New("edge-tts: the service closed the connection")
			}
			return s.format, &RefusedError{Code: closeErr.Code, Reason: closeErr.Text}
		}
		if err != nil {
			return s.format, t.readError(ctx, err)
		}

		switch kind {
		case websocket.TextMessage:
			path, body := parseTextFrame(data)
			switch path {
			case "audio.metadata":
				for _, b := range parseBoundaries(body) {
					onBoundary(b)
				}
			case "turn.end":
				return finish()
			}
		case websocket.BinaryMessage:
			path, body, err := parseBinaryFrame(data)
			if err != nil {
				return s.format, err
			}
			if path != "audio" || len(body) == 0 {
				continue
			}
			received = true
			if !onAudio(body) {
				return s.format, nil
			}
		}
	}
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
			"&Sec-MS-GEC-Version=" + t.secMSGECVersion +
			"&ConnectionId=" + randomHex()
		conn, resp, err := t.dialer.DialContext(ctx, url, withHeaders(wssHeaders(), t.headers))
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

// Duration estimates the length of the audio from its size and bitrate.
// MP3 is constant bitrate; formats without one (opus) assume 48 kbit/s.
func (t *EdgeTTS) Duration() (time.Duration, error) {
	audio, err := t.Bytes()
	if err != nil {
		return 0, err
	}
	return estimateDuration(len(audio), t.format), nil
}

// Extension returns the file extension of the output format, such as "mp3".
func (t *EdgeTTS) Extension() string {
	return extension(t.format)
}

// AudioInfo returns the size, format and estimated duration of the audio.
func (t *EdgeTTS) AudioInfo() (AudioInfo, error) {
	duration, err := t.Duration()
	if err != nil {
		return AudioInfo{}, err
	}
	return AudioInfo{Size: len(t.audio), Format: t.Extension(), EstimatedDuration: duration}, nil
}

// ToFile writes the audio to path, adding the extension of the output
// format unless path already ends with it, and returns the path it wrote.
func (t *EdgeTTS) ToFile(path string) (string, error) {
	audio, err := t.Bytes()
	if err != nil {
		return "", err
	}
	return writeAudio(path, t.Extension(), audio)
}

// WordBoundaries returns when each word of the last synthesis is spoken.
func (t *EdgeTTS) WordBoundaries() []WordBoundary {
	return t.words
}

// SentenceBoundaries returns when each sentence of the last synthesis is
// spoken, with the opening punctuation of the text (¿, ¡, «...) that the
// service leaves out.
func (t *EdgeTTS) SentenceBoundaries() []SentenceBoundary {
	return t.sentences
}

// SRT returns subtitles of the last synthesis, one cue per sentence.
func (t *EdgeTTS) SRT() string { return SRT(cues(t.sentences, t.words)) }

// VTT returns WebVTT subtitles of the last synthesis, one cue per sentence.
func (t *EdgeTTS) VTT() string { return VTT(cues(t.sentences, t.words)) }

// SaveMetadata writes the word boundaries to path as JSON.
func (t *EdgeTTS) SaveMetadata(path string) error {
	if len(t.words) == 0 {
		return ErrNoMetadata
	}
	data, err := json.MarshalIndent(t.words, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// cues returns the sentences, or the words when there are none.
func cues(sentences, words []Boundary) []Boundary {
	if len(sentences) > 0 {
		return sentences
	}
	return words
}

func writeAudio(path, ext string, audio []byte) (string, error) {
	path = filePath(path, ext)
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
