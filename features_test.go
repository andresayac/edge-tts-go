package edgetts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The same subtitles the tests of the Node package expect for the recorded session.
const recordedSRT = "1\n00:00:00,050 --> 00:00:02,013\nHello, world.\n\n" +
	"2\n00:00:02,013 --> 00:00:03,913\nIs this a test?\n\n" +
	"3\n00:00:03,913 --> 00:00:05,400\nYes!\n"

func TestCollectsTheSentenceBoundariesAndWritesSubtitles(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), loadSession(t).Text, "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	if got := words(tts.SentenceBoundaries()); !reflect.DeepEqual(got, []string{"Hello, world.", "Is this a test?", "Yes!"}) {
		t.Fatalf("sentences = %v", got)
	}
	if !reflect.DeepEqual(words(tts.WordBoundaries()), recordedWords) {
		t.Fatalf("words = %v", words(tts.WordBoundaries()))
	}
	if got := tts.SRT(); got != recordedSRT {
		t.Fatalf("SRT =\n%s", got)
	}
	if got := tts.VTT(); !strings.HasPrefix(got, "WEBVTT\n\n00:00:00.050 --> 00:00:02.013\nHello, world.\n") {
		t.Fatalf("VTT =\n%s", got)
	}
	if !strings.Contains(svc.conn(0).messages[0], `"sentenceBoundaryEnabled":true`) {
		t.Fatal("sentence boundaries not asked for")
	}
}

func TestGenerateReturnsEverythingWithoutTouchingTheInstance(t *testing.T) {
	svc, tts := startService(t, &fakeService{})
	r, err := tts.Generate(context.Background(), loadSession(t).Text, "en-US-AriaNeural", WithOutputFormat(Webm24khz16bitMonoOpus))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Audio, recordedAudio(t, svc.frames)) {
		t.Fatal("audio differs")
	}
	if r.Format != "webm" || r.OutputFormat != Webm24khz16bitMonoOpus || len(r.SentenceBoundaries) != 3 {
		t.Fatalf("result = %+v", r)
	}
	if !reflect.DeepEqual(words(r.WordBoundaries), recordedWords) || r.SRT() != recordedSRT {
		t.Fatal("boundaries differ")
	}
	if r.Duration != 122666666*time.Nanosecond {
		t.Fatalf("duration = %s", r.Duration)
	}
	path, err := r.ToFile(filepath.Join(t.TempDir(), "gen"))
	if err != nil || !strings.HasSuffix(path, "gen.webm") {
		t.Fatalf("path = %q, err = %v", path, err)
	}
	if _, err := tts.Bytes(); !errors.Is(err, ErrNoAudio) {
		t.Fatal("Generate changed the instance")
	}
}

func TestGenerateRunsConcurrently(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := tts.Generate(context.Background(), "hi", "en-US-AriaNeural")
			if err == nil && len(r.Audio) != 736 {
				err = errors.New("wrong audio size")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDoesNotRepeatTheExtensionOfTheFile(t *testing.T) {
	_, tts := startService(t, &fakeService{})
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(t.TempDir(), "speech.mp3")
	if path, err := tts.ToFile(want); err != nil || path != want {
		t.Fatalf("path = %q, err = %v", path, err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func TestUsesTheClientOptions(t *testing.T) {
	svc, _ := startService(t, &fakeService{})
	tts := New(
		WithWSSURL("ws"+strings.TrimPrefix(svc.server.URL, "http")+"/ws"),
		WithSecMSGECVersion("1-999.0.0.0"),
		WithHeader("X-Test", "yes"),
		WithHeader("User-Agent", "custom"),
		WithTimeout(5*time.Second),
	)
	if err := tts.Synthesize(context.Background(), "hi", "en-US-AriaNeural"); err != nil {
		t.Fatal(err)
	}
	c := svc.conn(0)
	if got := c.url.Query().Get("Sec-MS-GEC-Version"); got != "1-999.0.0.0" {
		t.Errorf("version = %q", got)
	}
	if c.headers.Get("X-Test") != "yes" || c.headers.Get("User-Agent") != "custom" {
		t.Errorf("headers = %v", c.headers)
	}
	if tts.idleTimeout != 5*time.Second {
		t.Errorf("timeout = %s", tts.idleTimeout)
	}
}

func TestCachesTheVoiceList(t *testing.T) {
	svc, _ := startService(t, &fakeService{voiceBody: rawVoices})
	ctx := context.Background()

	cached := New(WithVoicesURL(svc.server.URL + "/voices"))
	cached.Voices(ctx)
	voices, _ := cached.VoicesByLanguage(ctx, "es")
	voices[0].ShortName = "changed" // a copy, not the cache
	if again, _ := cached.Voices(ctx); again[1].ShortName != "es-CO-GonzaloNeural" {
		t.Fatal("the cache was changed through a result")
	}

	uncached := New(WithVoicesURL(svc.server.URL+"/voices"), WithVoicesCacheTTL(0))
	uncached.Voices(ctx)
	uncached.Voices(ctx)
	if n := len(svc.voiceRequests); n != 3 {
		t.Fatalf("%d voice requests, want 1 cached and 2 uncached", n)
	}
}

func TestPutsBackTheOpeningPunctuationTheServiceDrops(t *testing.T) {
	restore := sentenceTextRestorer("¿Cómo estás? ¡Qué bien! Dijo: «hola». (Nota) Normal.")
	var got []string
	for _, s := range []string{"Cómo estás?", "Qué bien!", "Dijo: «hola».", "Nota)", "Normal.", "not in source"} {
		got = append(got, restore(s))
	}
	want := []string{"¿Cómo estás?", "¡Qué bien!", "Dijo: «hola».", "(Nota)", "Normal.", "not in source"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestWritesSubtitles(t *testing.T) {
	cues := []Boundary{
		{Offset: 500000, Duration: 19625000, Text: "Hello, world."},
		{Offset: 0, Duration: 36_000_000_000, Text: "long"},
	}
	if got, want := SRT(cues), "1\n00:00:00,050 --> 00:00:02,013\nHello, world.\n\n2\n00:00:00,000 --> 01:00:00,000\nlong\n"; got != want {
		t.Fatalf("SRT = %q", got)
	}
	if got, want := VTT(cues), "WEBVTT\n\n00:00:00.050 --> 00:00:02.013\nHello, world.\n\n00:00:00.000 --> 01:00:00.000\nlong\n"; got != want {
		t.Fatalf("VTT = %q", got)
	}
}

func TestBuildsFilePaths(t *testing.T) {
	cases := map[string]string{"out": "out.mp3", "out.mp3": "out.mp3", "OUT.MP3": "OUT.MP3", "out.webm": "out.webm.mp3"}
	for in, want := range cases {
		if got := filePath(in, "mp3"); got != want {
			t.Errorf("filePath(%q) = %q, want %q", in, got, want)
		}
	}
}
