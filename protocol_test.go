package edgetts

import (
	"strings"
	"testing"
	"time"
)

func TestSecMSGECMatchesTheNodePackage(t *testing.T) {
	// SHA-256 of "134116992000000000" + TrustedClientToken: the Windows file
	// time of 2026-01-01 00:00:00 UTC, the start of the window of this time.
	at := time.Date(2026, 1, 1, 0, 4, 59, 0, time.UTC)
	const want = "42D1947403FD94975436C65DBFCA8003073F9CB5C3F1CE25AF8961A11D7C3DFE"
	if got := secMSGEC(at); got != want {
		t.Fatalf("secMSGEC = %s, want %s", got, want)
	}
	// Same five minute window, same token.
	if secMSGEC(at) != secMSGEC(at.Add(-4*time.Minute)) {
		t.Fatal("token changed inside the window")
	}
	if secMSGEC(at) == secMSGEC(at.Add(time.Second)) {
		t.Fatal("token did not change with the window")
	}
}

func TestFormatsTheProsody(t *testing.T) {
	cases := []struct {
		format func(string) (string, error)
		in     string
		want   string
	}{
		{formatPitch, "", "+0Hz"},
		{formatPitch, "5", "+5Hz"},
		{formatPitch, "-5", "-5Hz"},
		{formatPitch, "+10Hz", "+10Hz"},
		{formatPitch, "-2.5Hz", "-2.5Hz"},
		{formatRate, "", "+0%"},
		{formatRate, "50%", "+50%"},
		{formatRate, "-25%", "-25%"},
		{formatRate, "10", "+10%"},
		{formatVolume, "", "+0%"},
		{formatVolume, "90%", "+90%"},
		{formatVolume, "-100", "-100%"},
	}
	for _, c := range cases {
		got, err := c.format(c.in)
		if err != nil || got != c.want {
			t.Errorf("format(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []struct {
		format func(string) (string, error)
		in     string
	}{
		{formatPitch, "loud"}, {formatPitch, "10%"}, {formatPitch, "1e3"},
		{formatRate, "fast"}, {formatRate, "NaN"},
		{formatVolume, "101%"}, {formatVolume, "-101"}, {formatVolume, "quiet"},
	} {
		if got, err := bad.format(bad.in); err == nil {
			t.Errorf("format(%q) = %q, want an error", bad.in, got)
		}
	}
}

func TestBuildsSSMLWithoutCharactersXMLCannotHold(t *testing.T) {
	ssml := buildSSML("a\x0bb\x00c 'd' <e>", `x"y`, "+0Hz", "+0%", "+0%")
	for _, want := range []string{`<voice name="x&quot;y">`, "a b c &apos;d&apos; &lt;e&gt;"} {
		if !strings.Contains(ssml, want) {
			t.Errorf("ssml lacks %s:\n%s", want, ssml)
		}
	}
}

func TestParsesBinaryFrames(t *testing.T) {
	head := "X-RequestId:1\r\nPath:audio\r\n"
	data := append([]byte{0, byte(len(head))}, head...)
	data = append(data, 1, 2, 3)
	path, body, err := parseBinaryFrame(data)
	if err != nil || path != "audio" || string(body) != "\x01\x02\x03" {
		t.Fatalf("path = %q, body = %v, err = %v", path, body, err)
	}
	if _, _, err := parseBinaryFrame([]byte{0, 9, 'x'}); err == nil {
		t.Fatal("accepted headers longer than the frame")
	}
	if _, _, err := parseBinaryFrame([]byte{0}); err == nil {
		t.Fatal("accepted a one byte frame")
	}
}

func TestNamesTheFileAfterTheFormat(t *testing.T) {
	cases := map[string]string{
		"audio-24khz-48kbitrate-mono-mp3": "mp3",
		"webm-24khz-16bit-mono-opus":      "webm",
		"ogg-24khz-16bit-mono-opus":       "ogg",
		"riff-24khz-16bit-mono-pcm":       "wav",
		"raw-24khz-16bit-mono-pcm":        "pcm",
		"raw-8khz-8bit-mono-alaw":         "alaw",
		"raw-8khz-8bit-mono-mulaw":        "mulaw",
		"raw-16khz-16bit-mono-truesilk":   "silk",
		"g722-16khz-64kbps":               "g722",
		"amr-wb-16000hz":                  "amr",
		"something-else":                  "audio",
	}
	for format, want := range cases {
		if got := (&EdgeTTS{format: format}).Extension(); got != want {
			t.Errorf("Extension(%s) = %s, want %s", format, got, want)
		}
	}
}
