# Edge TTS for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/andresayac/edge-tts-go.svg)](https://pkg.go.dev/github.com/andresayac/edge-tts-go)

**edge-tts-go** turns text into natural-sounding speech with the online text-to-speech service of Microsoft Edge, without Edge, Windows or an API key.

## Available in three languages

The same library for the Edge text-to-speech service, in Node.js, PHP and Go:

| Language | Install | Repository |
| --- | --- | --- |
| Node.js and browsers | `npm install @andresaya/edge-tts` | [andresayac/edge-tts](https://github.com/andresayac/edge-tts) |
| PHP | `composer require afaya/edge-tts` | [andresayac/edge-tts-php](https://github.com/andresayac/edge-tts-php) |
| **Go** (this one) | `go get github.com/andresayac/edge-tts-go` | [andresayac/edge-tts-go](https://github.com/andresayac/edge-tts-go) |

## Features

- **Text-to-speech** with more than 300 neural voices in over 100 locales.
- **Streaming**: get the audio as it arrives with a `range` loop.
- **Word and sentence boundaries**: know when each word and sentence is spoken.
- **Subtitles**: SRT or WebVTT, one cue per sentence.
- **Voice filtering** by language and gender, with the list cached for 10 minutes.
- **Pitch, rate and volume** control.
- **Configurable**: override the endpoint, headers and timeout without waiting for a release.
- **Context support**: cancel or time out any request.
- **Concurrent**: `Generate` and `Voices` are safe from many goroutines.
- **Command-line tool** included.
- One dependency ([gorilla/websocket](https://github.com/gorilla/websocket)).

## Installation

```bash
go get github.com/andresayac/edge-tts-go
```

Requires Go 1.23 or later.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	edgetts "github.com/andresayac/edge-tts-go"
)

func main() {
	tts := edgetts.New()

	err := tts.Synthesize(context.Background(), "Hello, world!", "en-US-AriaNeural")
	if err != nil {
		log.Fatal(err)
	}

	path, err := tts.ToFile("hello") // the extension comes from the format
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("saved", path) // saved hello.mp3
}
```

## Command-line interface

```bash
go install github.com/andresayac/edge-tts-go/cmd/edge-tts@latest
```

```bash
# Synthesize text to hello.mp3
edge-tts synthesize -t "Hello, world!" -o hello

# Read the text from a file, with another voice and prosody, and write subtitles too
edge-tts synthesize -f story.txt -v es-CO-SalomeNeural -r +10% -p -5Hz -o story --subtitles story.srt

# Read from stdin, WebM/Opus output, WebVTT subtitles
cat story.txt | edge-tts synthesize -f - --format webm-24khz-16bit-mono-opus -o story --subtitles story.vtt

# List the voices, optionally filtered
edge-tts voice-list
edge-tts voice-list --locale es-CO --gender female
```

| Option | Description | Default |
| --- | --- | --- |
| `-t, --text` | Text to convert to audio | |
| `-f, --file` | Input file (text), or `-` for stdin | |
| `-v, --voice` | Voice to use | `en-US-AriaNeural` |
| `-r, --rate` | Rate of speech | `0%` |
| `-l, --volume` | Volume of speech | `0%` |
| `-p, --pitch` | Pitch of speech | `0Hz` |
| `-o, --output` | Output file name; the extension is added when missing | `output_<timestamp>` |
| `--format` | Output format | `audio-24khz-48kbitrate-mono-mp3` |
| `--subtitles` | Write subtitles: `.srt`, or `.vtt` for WebVTT | |

## API

### Configuration

Every option is optional. The defaults come from the constants of the package, so when Microsoft changes the service you can set the new values yourself instead of waiting for a release.

```go
tts := edgetts.New(
	edgetts.WithWSSURL("wss://api.msedgeservices.com/tts/cognitiveservices/websocket/v1"),
	edgetts.WithVoicesURL("https://speech.platform.bing.com/consumer/speech/synthesize/readaloud/voices/list"),
	edgetts.WithSecMSGECVersion("1-155.0.4283.45"),
	edgetts.WithHeader("User-Agent", "..."), // added to the default headers, or replacing one
	edgetts.WithTimeout(30*time.Second),     // without data from the service
	edgetts.WithVoicesCacheTTL(10*time.Minute), // 0 fetches the voice list every time
)
```

A `context` bounds a whole call; `WithTimeout` only how long the service may stay silent.

### Voices

```go
ctx := context.Background()

voices, err := tts.Voices(ctx)                   // every voice
english, err := tts.VoicesByLanguage(ctx, "en")  // en-US, en-GB, en-IN...
american, err := tts.VoicesByLanguage(ctx, "en-US")
female, err := tts.VoicesByGender(ctx, "Female")

for _, v := range voices {
	fmt.Println(v.ShortName, v.FriendlyName) // en-US-AriaNeural Aria (Neural) - en-US
}
```

Each `Voice` has `Name`, `ShortName`, `DisplayName`, `LocalName`, `Gender`, `Locale`, `LocaleName`, `SecondaryLocaleList`, `VoiceType`, `VoiceTag` and `FriendlyName`.

### Synthesis

```go
err := tts.Synthesize(ctx, "Hello, world!", "en-US-AriaNeural",
	edgetts.WithRate("+50%"),     // -100% and up
	edgetts.WithVolume("-10%"),   // -100% to +100%
	edgetts.WithPitch("+20Hz"),   // -100Hz to +100Hz
	edgetts.WithOutputFormat(edgetts.Audio24khz96kbitrateMonoMP3),
)
```

Every option is optional. A bare number works too: `WithRate("50")` is `+50%` and `WithPitch("-5")` is `-5Hz`. An empty voice means `edgetts.DefaultVoice`.

#### Output formats

| Constant | Format | Extension |
| --- | --- | --- |
| `Audio24khz48kbitrateMonoMP3` (default) | `audio-24khz-48kbitrate-mono-mp3` | `.mp3` |
| `Audio24khz96kbitrateMonoMP3` | `audio-24khz-96kbitrate-mono-mp3` | `.mp3` |
| `Webm24khz16bitMonoOpus` | `webm-24khz-16bit-mono-opus` | `.webm` |

These are the only formats the service accepts. It refuses the other Azure formats (WAV, PCM, OGG, 48 kHz MP3...) with "Unsupported Edge output format".

#### Matching the voice to the text

Voices are locale specific. A voice can only read text written in the script of its own language, so passing Chinese, Cyrillic, Arabic or Devanagari text to an English voice returns no audio. `Synthesize` then returns `edgetts.ErrEmptyAudio`:

```go
err := tts.Synthesize(ctx, "这是一个测试", "en-US-AriaNeural")
errors.Is(err, edgetts.ErrEmptyAudio) // true

err = tts.Synthesize(ctx, "这是一个测试", "zh-CN-XiaoxiaoNeural") // works
```

### Streaming

`Stream` returns an iterator that yields the audio as it arrives, so you can start playing or sending it before the synthesis ends:

```go
for chunk, err := range tts.Stream(ctx, "Long text to stream...", "en-US-AriaNeural") {
	if err != nil {
		return err
	}
	w.Write(chunk) // a file, an http.ResponseWriter, a player...
}
```

Breaking out of the loop closes the connection.

### Everything in one result

`Generate` returns the audio, the boundaries and the duration in a `Result`, and leaves the `EdgeTTS` as it was, so many goroutines can share one client:

```go
r, err := tts.Generate(ctx, "¿Cómo estás? ¡Qué bien!", "es-CO-SalomeNeural")

r.Audio              // []byte
r.Format             // "mp3"
r.OutputFormat       // "audio-24khz-48kbitrate-mono-mp3"
r.WordBoundaries
r.SentenceBoundaries
r.Duration           // estimated

path, err := r.ToFile("greeting") // greeting.mp3
srt := r.SRT()                    // or r.VTT()
```

### Getting the audio

After `Synthesize` (or a full `Stream`):

```go
audio, err := tts.Bytes()            // []byte
encoded, err := tts.Base64()         // string
path, err := tts.ToFile("out/audio") // out/audio.mp3
path, err = tts.ToFile("out/audio.mp3") // out/audio.mp3, the extension is not repeated
info, err := tts.AudioInfo()         // {Size: 46944, Format: "mp3", EstimatedDuration: 3.912s}
d, err := tts.Duration()             // estimated from the size and bitrate
ext := tts.Extension()               // "mp3"
```

### Word and sentence boundaries

```go
for _, w := range tts.WordBoundaries() {
	fmt.Printf("%s  %s\n", w.Start(), w.Text)
}
// 45.458ms  Hola
// 613.625ms  cómo
// ...

tts.SentenceBoundaries() // the same, one per sentence
err := tts.SaveMetadata("words.json")
```

`Offset` and `Duration` are in 100-nanosecond units, as the service sends them; `Start()` and `End()` return them as `time.Duration`.

The service leaves the opening punctuation out of its sentences, so "¿Cómo estás?" comes back as "Cómo estás?". The library puts it back from your text: ¿ ¡ « " ' “ ‘ ( [.

### Subtitles

```go
os.WriteFile("speech.srt", []byte(tts.SRT()), 0o644) // one cue per sentence
os.WriteFile("speech.vtt", []byte(tts.VTT()), 0o644)

edgetts.SRT(tts.WordBoundaries()) // one cue per word
```

```
1
00:00:00,050 --> 00:00:01,600
¿Cómo estás?

2
00:00:01,600 --> 00:00:02,950
¡Qué bien!
```

### Errors

| Error | When |
| --- | --- |
| `ErrEmptyText` | An empty text; nothing is sent |
| `ErrNoAudio` | Asking for audio before a synthesis |
| `ErrEmptyAudio` | The voice cannot read the text |
| `ErrNoMetadata` | `SaveMetadata` without word boundaries |
| `*HandshakeError` | The service refused the connection; holds the HTTP status |
| `*RefusedError` | The service refused the request, such as "Unsupported voice xx-XX-NobodyNeural."; holds the close code and the reason |

A machine whose clock is off by more than a few minutes is refused by the service. The client then corrects its clock from the response and tries once more.

### Concurrency

`Synthesize` and `Stream` keep the audio of the last synthesis in the `EdgeTTS`, so they are not safe for concurrent use. `Generate` and `Voices` are: share one client and call `Generate` from as many goroutines as you need.

## Examples

- [`examples/basic`](examples/basic): synthesize to a file and list voices.
- [`examples/stream`](examples/stream): stream the audio to a file.
- [`examples/subtitles`](examples/subtitles): generate the audio and its subtitles in one call.

```bash
go run ./examples/basic
```

## License

GPL-3.0. See [LICENSE](LICENSE).
