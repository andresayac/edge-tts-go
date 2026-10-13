package edgetts

import "net/http"

const (
	// TrustedClientToken is the token Edge sends to the read aloud service.
	TrustedClientToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"

	BaseURL = "https://speech.platform.bing.com/consumer/speech/synthesize/readaloud"
	// NOTE: the token goes as Ocp-Apim-Subscription-Key here; Edge user agents only.
	WSSURL    = "wss://api.msedgeservices.com/tts/cognitiveservices/websocket/v1"
	VoicesURL = "https://speech.platform.bing.com/consumer/speech/synthesize/readaloud/voices/list"

	ChromiumFullVersion  = "155.0.0.0"
	ChromiumMajorVersion = "155"
	SecMSGECVersion      = "1-155.0.4283.45"

	// DefaultVoice is used when Synthesize or Stream get an empty voice.
	DefaultVoice = "en-US-AnaNeural"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" +
		ChromiumFullVersion + " Safari/537.36 Edg/" + ChromiumFullVersion
)

// Output formats accepted by WithOutputFormat.
// https://learn.microsoft.com/en-us/azure/ai-services/speech-service/rest-text-to-speech?tabs=nonstreaming
const (
	Audio24khz48kbitrateMonoMP3 = "audio-24khz-48kbitrate-mono-mp3"
	Audio24khz96kbitrateMonoMP3 = "audio-24khz-96kbitrate-mono-mp3"
	Webm24khz16bitMonoOpus      = "webm-24khz-16bit-mono-opus"
)

func baseHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", userAgent)
	h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Cookie", "MUID="+token32())
	return h
}

// withHeaders returns base with the headers of extra added or replaced.
func withHeaders(base, extra http.Header) http.Header {
	for name, values := range extra {
		base[name] = values
	}
	return base
}

func wssHeaders() http.Header {
	h := baseHeaders()
	h.Set("Pragma", "no-cache")
	h.Set("Cache-Control", "no-cache")
	h.Set("Origin", "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold")
	return h
}

func voiceHeaders() http.Header {
	h := baseHeaders()
	h.Set("Sec-CH-UA", `"Microsoft Edge";v="`+ChromiumMajorVersion+`", "Chromium";v="`+ChromiumMajorVersion+`", "Not(A:Brand";v="24"`)
	h.Set("Sec-CH-UA-Mobile", "?0")
	h.Set("Sec-CH-UA-Platform", `"Windows"`)
	h.Set("Accept", "*/*")
	h.Set("Sec-Fetch-Site", "none")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Dest", "empty")
	// Plain JSON; with gzip/br/zstd asked for by hand Go would not decode it.
	h.Set("Accept-Encoding", "identity")
	return h
}
