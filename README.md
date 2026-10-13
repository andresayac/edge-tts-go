# Edge TTS for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/andresayac/edge-tts-go.svg)](https://pkg.go.dev/github.com/andresayac/edge-tts-go)

**edge-tts-go** turns text into natural-sounding speech with the online text-to-speech service of Microsoft Edge, without Edge, Windows or an API key.

It is the Go port of [`@andresaya/edge-tts`](https://github.com/andresayac/edge-tts) for Node.js. A PHP version lives in [`edge-tts-php`](https://github.com/andresayac/edge-tts-php).

## Features

- **Text-to-speech** with more than 300 neural voices in over 100 locales.
- **Streaming**: get the audio as it arrives with a `range` loop.
- **Word boundaries**: know when each word is spoken.
- **Voice filtering** by language and gender.
- **Pitch, rate and volume** control.
- **Context support**: cancel or time out any request.
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

# Read the text from a file, with another voice and prosody
edge-tts synthesize -f story.txt -v es-CO-SalomeNeural -r +10% -p -5Hz -o story

# List the voices
edge-tts voice-list
```

| Option | Description | Default |
| --- | --- | --- |
| `-t, --text` | Text to convert to audio | |
| `-f, --file` | Input file (text) | |
| `-v, --voice` | Voice to use | `en-US-AriaNeural` |
| `-r, --rate` | Rate of speech | `0%` |
| `-l, --volume` | Volume of speech | `0%` |
| `-p, --pitch` | Pitch of speech | `0Hz` |
| `-o, --output` | Output file name, without extension | `output_<timestamp>` |

## API

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

### Getting the audio

After `Synthesize` (or a full `Stream`):

```go
audio, err := tts.Bytes()            // []byte
encoded, err := tts.Base64()         // string
path, err := tts.ToFile("out/audio") // out/audio.mp3
info, err := tts.AudioInfo()         // {Size: 46944, Format: "mp3", EstimatedDuration: 3.912s}
d, err := tts.Duration()             // estimated from the size and bitrate
ext := tts.Extension()               // "mp3"
```

### Word boundaries

```go
for _, w := range tts.WordBoundaries() {
	fmt.Printf("%s  %s\n", w.Start(), w.Text)
}
// 45.458ms  Hola
// 613.625ms  cómo
// ...

err := tts.SaveMetadata("words.json")
```

`Offset` and `Duration` are in 100-nanosecond units, as the service sends them; `Start()` and `End()` return them as `time.Duration`.

### Errors

| Error | When |
| --- | --- |
| `ErrNoAudio` | Asking for audio before a synthesis |
| `ErrEmptyAudio` | The voice cannot read the text |
| `ErrNoMetadata` | `SaveMetadata` without word boundaries |
| `*HandshakeError` | The service refused the connection; holds the HTTP status |

A machine whose clock is off by more than a few minutes is refused by the service. The client then corrects its clock from the response and tries once more.

### Concurrency

An `EdgeTTS` keeps the audio of its last synthesis, so it is not safe for concurrent use. Create one per goroutine; they are cheap.

## Examples

- [`examples/basic`](examples/basic): synthesize to a file and list voices.
- [`examples/stream`](examples/stream): stream the audio to a file.

```bash
go run ./examples/basic
```

## License

GPL-3.0. See [LICENSE](LICENSE).
