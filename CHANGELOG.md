# Changelog

## v0.2.0

Everything from v0.1 keeps compiling: `WordBoundary` is now an alias of the new `Boundary` type, with the same fields and methods.

### Added
- `New(opts ...ClientOption)`: `WithWSSURL`, `WithVoicesURL`, `WithSecMSGECVersion`, `WithHeader`, `WithTimeout` and `WithVoicesCacheTTL`, so a change on Microsoft's side can be handled without a new release.
- `Generate` returns the audio, the word and sentence boundaries and the duration in a `Result`, without changing the `EdgeTTS`. It is safe to call from many goroutines.
- Sentence boundaries (`SentenceBoundaries`) and subtitles: `SRT`, `VTT`, and the `SRT` and `VTT` functions. Opening punctuation such as ¿ and ¡, which the service drops, is restored.
- The voice list is cached for 10 minutes per client.
- CLI: `--format`, `--subtitles`, `-f -` to read from stdin, and `voice-list --locale --gender`.

### Fixed
- `ToFile("speech.mp3")` wrote `speech.mp3.mp3`.

## v0.1.1

- An unsupported voice or output format returns a `RefusedError` with the reason the service gives, such as "Unsupported voice xx-XX-NobodyNeural.".
- An empty text returns `ErrEmptyText` without connecting.
- A connection that drops reads as such, not as a refusal.

## v0.1.0

- First release: synthesis, streaming, word boundaries, voices and the `edge-tts` command, ported from the Node.js package `@andresaya/edge-tts`.
