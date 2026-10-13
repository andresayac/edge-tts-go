package edgetts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

var recordedWords = []string{"Hello", "world", "Is", "this", "a", "test", "Yes"}

func words(bs []WordBoundary) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Text
	}
	return out
}

func TestSynthesizeKeepsTheAudioOfEveryBinaryFrame(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), loadSession(t).Text, "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	audio, err := tts.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if want := recordedAudio(t, loadSession(t).Frames); !bytes.Equal(audio, want) {
		t.Fatalf("audio is %d bytes, want the %d recorded", len(audio), len(want))
	}
}

func TestSynthesizeCollectsTheWordBoundaries(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	got := tts.WordBoundaries()
	if !reflect.DeepEqual(words(got), recordedWords) {
		t.Fatalf("words = %v, want %v", words(got), recordedWords)
	}
	want := WordBoundary{Type: "WordBoundary", Offset: 500000, Duration: 4500000, Text: "Hello"}
	if got[0] != want {
		t.Fatalf("first boundary = %+v, want %+v", got[0], want)
	}
	if got[0].Start() != 50*time.Millisecond || got[0].End() != 500*time.Millisecond {
		t.Fatalf("first word spans %s to %s", got[0].Start(), got[0].End())
	}
}

func TestConnectsWithTheTokenTheSignatureAndEdgeHeaders(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	c := svc.conn(0)
	q := c.url.Query()
	if got := q.Get("Ocp-Apim-Subscription-Key"); got != TrustedClientToken {
		t.Errorf("token = %q", got)
	}
	if got := q.Get("Sec-MS-GEC"); !regexp.MustCompile(`^[0-9A-F]{64}$`).MatchString(got) {
		t.Errorf("Sec-MS-GEC = %q", got)
	}
	if got := q.Get("Sec-MS-GEC-Version"); got != SecMSGECVersion {
		t.Errorf("Sec-MS-GEC-Version = %q", got)
	}
	if q.Get("ConnectionId") == "" {
		t.Error("no ConnectionId")
	}
	if ua := c.headers.Get("User-Agent"); !strings.Contains(ua, "Edg/") {
		t.Errorf("User-Agent = %q", ua)
	}
	if origin := c.headers.Get("Origin"); !strings.HasPrefix(origin, "chrome-extension://") {
		t.Errorf("Origin = %q", origin)
	}
}

func TestSendsTheConfigThenTheEscapedSSMLWithTheProsody(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	err := tts.Synthesize(context.Background(), `a < b & "c"`, "es-CO-SalomeNeural",
		WithPitch("+5Hz"), WithRate("10"), WithVolume("-20%"))
	if err != nil {
		t.Fatal(err)
	}
	msgs := svc.conn(0).messages
	config, ssml := msgs[0], msgs[1]
	for _, want := range []string{"Path:speech.config", `"outputFormat":"audio-24khz-48kbitrate-mono-mp3"`} {
		if !strings.Contains(config, want) {
			t.Errorf("config lacks %s:\n%s", want, config)
		}
	}
	for _, want := range []string{
		"Path:ssml",
		`<voice name="es-CO-SalomeNeural">`,
		`<prosody pitch="+5Hz" rate="+10%" volume="-20%">`,
		"a &lt; b &amp; &quot;c&quot;",
	} {
		if !strings.Contains(ssml, want) {
			t.Errorf("ssml lacks %s:\n%s", want, ssml)
		}
	}
}

func TestPassesTheOutputFormatAndNamesTheFileAfterIt(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural", WithOutputFormat(Webm24khz16bitMonoOpus)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(svc.conn(0).messages[0], `"outputFormat":"webm-24khz-16bit-mono-opus"`) {
		t.Errorf("config: %s", svc.conn(0).messages[0])
	}
	base := filepath.Join(t.TempDir(), "out")
	path, err := tts.ToFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if path != base+".webm" {
		t.Fatalf("path = %q", path)
	}
	written, _ := os.ReadFile(path)
	if !bytes.Equal(written, recordedAudio(t, svc.frames)) {
		t.Fatal("file does not hold the recorded audio")
	}
}

func TestExportsBase64AndAudioInfo(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	b64, err := tts.Base64()
	if err != nil {
		t.Fatal(err)
	}
	if want := base64.StdEncoding.EncodeToString(recordedAudio(t, loadSession(t).Frames)); b64 != want {
		t.Fatal("base64 differs")
	}
	info, err := tts.AudioInfo()
	if err != nil {
		t.Fatal(err)
	}
	// 736 bytes at 48 kbit/s.
	want := AudioInfo{Size: 736, Format: "mp3", EstimatedDuration: 122666666 * time.Nanosecond}
	if info != want {
		t.Fatalf("info = %+v, want %+v", info, want)
	}
}

func TestStartsFromScratchOnEverySynthesis(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	for range 2 {
		if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
			t.Fatal(err)
		}
	}
	audio, _ := tts.Bytes()
	if len(audio) != 736 || len(tts.WordBoundaries()) != len(recordedWords) {
		t.Fatalf("%d bytes and %d words after two runs", len(audio), len(tts.WordBoundaries()))
	}
}

func TestStreamsTheSameAudio(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	var streamed []byte
	for chunk, err := range tts.Stream(context.Background(), "hi", "en-US-AriaNeural") {
		if err != nil {
			t.Fatal(err)
		}
		streamed = append(streamed, chunk...)
	}
	if !bytes.Equal(streamed, recordedAudio(t, svc.frames)) {
		t.Fatal("streamed audio differs")
	}
	if audio, _ := tts.Bytes(); len(audio) != 736 {
		t.Fatalf("kept %d bytes", len(audio))
	}
	if !reflect.DeepEqual(words(tts.WordBoundaries()), recordedWords) {
		t.Fatalf("words = %v", words(tts.WordBoundaries()))
	}
}

func TestStreamStopsWhenTheLoopBreaks(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	n := 0
	for _, err := range tts.Stream(context.Background(), "hi", "en-US-AriaNeural") {
		if err != nil {
			t.Fatal(err)
		}
		n++
		break
	}
	if n != 1 {
		t.Fatalf("got %d chunks", n)
	}
}

func TestRejectsWhenTheHandshakeIsRefused(t *testing.T) {
	_, tts := startService(t, &fakeService{handshakeStatus: 403})
	err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural")
	var he *HandshakeError
	if !errors.As(err, &he) || he.StatusCode != 403 {
		t.Fatalf("err = %v", err)
	}
}

func TestCorrectsTheClockFromARefusal(t *testing.T) {
	// The service is an hour ahead and only takes signatures for its own time.
	ahead := func() time.Time { return time.Now().Add(time.Hour) }
	accept := func(u *url.URL) bool { return u.Query().Get("Sec-MS-GEC") == secMSGEC(ahead()) }
	svc, tts := startService(t, &fakeService{date: ahead, accept: accept})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	if len(svc.connections) != 1 {
		t.Fatalf("%d connections", len(svc.connections))
	}
}

func TestRejectsAnInvalidPitchRateOrVolume(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	ctx := context.Background()
	cases := map[string]Option{
		"invalid pitch":  WithPitch("loud"),
		"invalid rate":   WithRate("fast"),
		"invalid volume": WithVolume("500%"),
	}
	for want, opt := range cases {
		if err := tts.Synthesize(ctx, "hi", "v", opt); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if len(svc.connections) != 0 {
		t.Error("connected with invalid options")
	}
}

func TestReportsTheVoiceThatCannotReadTheText(t *testing.T) {
	var frames []frame
	for _, f := range loadSession(t).Frames {
		if f.Text != nil {
			frames = append(frames, f)
		}
	}
	// One empty audio frame, as the service sends for an unreadable text.
	frames = append([]frame{{Binary: base64.StdEncoding.EncodeToString([]byte("\x00\x0cPath:audio\r\n"))}}, frames...)
	_, tts := startService(t, &fakeService{frames: frames})
	if err := tts.Synthesize(context.Background(), "这是一个测试", "en-US-AriaNeural"); !errors.Is(err, ErrEmptyAudio) {
		t.Fatalf("err = %v", err)
	}
}

func TestTimesOutWhenTheServiceIsSilent(t *testing.T) {
	_, tts := startService(t, &fakeService{silent: true})
	tts.idleTimeout = 100 * time.Millisecond
	err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural")
	if err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("err = %v", err)
	}
}

func TestStopsWhenTheContextIsCanceled(t *testing.T) {
	_, tts := startService(t, &fakeService{silent: true})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := tts.Synthesize(ctx, "hi", "en-US-AriaNeural"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorsBeforeAnySynthesis(t *testing.T) {
	tts := New()
	if _, err := tts.Bytes(); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v", err)
	}
	if err := tts.SaveMetadata(filepath.Join(t.TempDir(), "m.json")); !errors.Is(err, ErrNoMetadata) {
		t.Fatalf("err = %v", err)
	}
}

func TestSavesTheMetadataAsJSON(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "meta.json")
	if err := tts.SaveMetadata(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var got []map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	first := map[string]any{"type": "WordBoundary", "offset": 500000.0, "duration": 4500000.0, "text": "Hello"}
	if len(got) != len(recordedWords) || !reflect.DeepEqual(got[0], first) {
		t.Fatalf("metadata = %s", data)
	}
}

const rawVoices = `[
 {"Name":"Microsoft Server Speech Text to Speech Voice (en-US, AriaNeural)","ShortName":"en-US-AriaNeural","Gender":"Female","Locale":"en-US","FriendlyName":"Microsoft Aria Online (Natural) - English (United States)","VoiceTag":{"ContentCategories":["News","Novel"],"VoicePersonalities":["Positive","Confident"]}},
 {"Name":"Microsoft Server Speech Text to Speech Voice (es-CO, GonzaloNeural)","ShortName":"es-CO-GonzaloNeural","Gender":"Male","Locale":"es-CO"}
]`

// Produced by normalizeVoices of the Node package for rawVoices.
const normalizedVoices = `[{"Name":"en-US-AriaNeural","DisplayName":"Aria","LocalName":"Aria","ShortName":"en-US-AriaNeural","Gender":"Female","Locale":"en-US","LocaleName":"en-US","SecondaryLocaleList":[],"VoiceType":"Neural","VoiceTag":{"TailoredScenarios":["News","Novel"],"VoicePersonalities":["Positive","Confident"]},"FriendlyName":"Aria (Neural) - en-US"},{"Name":"es-CO-GonzaloNeural","DisplayName":"Gonzalo","LocalName":"Gonzalo","ShortName":"es-CO-GonzaloNeural","Gender":"Male","Locale":"es-CO","LocaleName":"es-CO","SecondaryLocaleList":[],"VoiceType":"Neural","VoiceTag":{"TailoredScenarios":[],"VoicePersonalities":[]},"FriendlyName":"Gonzalo (Neural) - es-CO"}]`

func TestNormalizesTheVoicesLikeTheNodePackage(t *testing.T) {
	svc, tts := startService(t, &fakeService{voiceBody: rawVoices})
	voices, err := tts.Voices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(voices)
	if string(got) != normalizedVoices {
		t.Fatalf("voices =\n%s\nwant\n%s", got, normalizedVoices)
	}
	req := svc.voiceRequests[0]
	if req.URL.Query().Get("TrustedClientToken") != TrustedClientToken || req.URL.Query().Get("Sec-MS-GEC") == "" {
		t.Errorf("query = %s", req.URL.RawQuery)
	}
	if req.Header.Get("Accept-Encoding") != "identity" || req.Header.Get("Sec-CH-UA") == "" {
		t.Errorf("headers = %v", req.Header)
	}
}

func TestAcceptsAVoiceListWrappedInAnObject(t *testing.T) {
	_, tts := startService(t, &fakeService{voiceBody: `{"voices":` + rawVoices + `}`})
	voices, err := tts.Voices(context.Background())
	if err != nil || len(voices) != 2 {
		t.Fatalf("%d voices, err = %v", len(voices), err)
	}
}

func TestFiltersVoicesByLanguageAndGender(t *testing.T) {
	_, tts := startService(t, &fakeService{voiceBody: rawVoices})
	ctx := context.Background()
	es, err := tts.VoicesByLanguage(ctx, "es")
	if err != nil || len(es) != 1 || es[0].ShortName != "es-CO-GonzaloNeural" {
		t.Fatalf("es = %v, err = %v", es, err)
	}
	female, err := tts.VoicesByGender(ctx, "Female")
	if err != nil || len(female) != 1 || female[0].ShortName != "en-US-AriaNeural" {
		t.Fatalf("female = %v, err = %v", female, err)
	}
}

func TestReportsAFailedVoiceListRequest(t *testing.T) {
	_, tts := startService(t, &fakeService{voiceStatus: 500})
	if _, err := tts.Voices(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}
