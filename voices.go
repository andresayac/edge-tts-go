package edgetts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Voice describes a voice of the service.
type Voice struct {
	Name                string   `json:"Name"`
	DisplayName         string   `json:"DisplayName"`
	LocalName           string   `json:"LocalName"`
	ShortName           string   `json:"ShortName"`
	Gender              string   `json:"Gender"`
	Locale              string   `json:"Locale"`
	LocaleName          string   `json:"LocaleName"`
	SecondaryLocaleList []string `json:"SecondaryLocaleList"`
	VoiceType           string   `json:"VoiceType"`
	VoiceTag            VoiceTag `json:"VoiceTag"`
	FriendlyName        string   `json:"FriendlyName"`
}

// VoiceTag lists what a voice is suited for.
type VoiceTag struct {
	TailoredScenarios  []string `json:"TailoredScenarios"`
	VoicePersonalities []string `json:"VoicePersonalities"`
}

// Voices returns every voice of the service. The list is kept for the
// time set with WithVoicesCacheTTL, 10 minutes by default.
func (t *EdgeTTS) Voices(ctx context.Context) ([]Voice, error) {
	t.voicesMu.Lock()
	defer t.voicesMu.Unlock()
	if t.voicesCache != nil && time.Now().Before(t.voicesExpires) {
		return slices.Clone(t.voicesCache), nil
	}
	voices, err := t.fetchVoices(ctx)
	if err != nil {
		return nil, err
	}
	if t.voicesCacheTTL > 0 {
		t.voicesCache, t.voicesExpires = voices, time.Now().Add(t.voicesCacheTTL)
	}
	return slices.Clone(voices), nil
}

func (t *EdgeTTS) fetchVoices(ctx context.Context) ([]Voice, error) {
	for attempt := 0; ; attempt++ {
		url := t.voicesURL +
			"?TrustedClientToken=" + TrustedClientToken +
			"&Sec-MS-GEC=" + secMSGEC(now()) +
			"&Sec-MS-GEC-Version=" + t.secMSGECVersion
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header = withHeaders(voiceHeaders(), t.headers)

		resp, err := t.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("edge-tts: voice list request: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("edge-tts: voice list request: %w", err)
		}
		if resp.StatusCode == http.StatusForbidden && attempt == 0 && adjustClock(resp.Header) {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("edge-tts: voice list request failed with status %d", resp.StatusCode)
		}
		raw, err := decodeVoices(body)
		if err != nil {
			return nil, fmt.Errorf("edge-tts: invalid voice list JSON: %w", err)
		}
		return normalizeVoices(raw), nil
	}
}

// VoicesByLanguage returns the voices whose locale starts with locale, so
// "en" matches every English voice and "en-US" only the American ones.
func (t *EdgeTTS) VoicesByLanguage(ctx context.Context, locale string) ([]Voice, error) {
	return t.filterVoices(ctx, func(v Voice) bool { return strings.HasPrefix(v.Locale, locale) })
}

// VoicesByGender returns the voices of gender, "Male" or "Female".
func (t *EdgeTTS) VoicesByGender(ctx context.Context, gender string) ([]Voice, error) {
	return t.filterVoices(ctx, func(v Voice) bool { return v.Gender == gender })
}

func (t *EdgeTTS) filterVoices(ctx context.Context, keep func(Voice) bool) ([]Voice, error) {
	voices, err := t.Voices(ctx)
	if err != nil {
		return nil, err
	}
	var out []Voice
	for _, v := range voices {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out, nil
}

type rawVoice struct {
	Name                string   `json:"Name"`
	ShortName           string   `json:"ShortName"`
	Gender              string   `json:"Gender"`
	Locale              string   `json:"Locale"`
	LocaleName          string   `json:"LocaleName"`
	DisplayName         string   `json:"DisplayName"`
	FriendlyName        string   `json:"FriendlyName"`
	VoiceType           string   `json:"VoiceType"`
	SecondaryLocaleList []string `json:"SecondaryLocaleList"`
	VoiceTag            struct {
		TailoredScenarios  []string `json:"TailoredScenarios"`
		ContentCategories  []string `json:"ContentCategories"`
		VoicePersonalities []string `json:"VoicePersonalities"`
	} `json:"VoiceTag"`
}

// decodeVoices reads either a bare array or an object with a voices array.
func decodeVoices(body []byte) ([]rawVoice, error) {
	var voices []rawVoice
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		err := json.Unmarshal(trimmed, &voices)
		return voices, err
	}
	var wrapped struct {
		Lower []rawVoice `json:"voices"`
		Upper []rawVoice `json:"Voices"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Lower != nil {
		return wrapped.Lower, nil
	}
	return wrapped.Upper, nil
}

var (
	localePrefix    = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}-`)
	neuralHDSuffix  = regexp.MustCompile(`NeuralHD$`)
	neuralSuffix    = regexp.MustCompile(`Neural$`)
	neuralHD        = regexp.MustCompile(`(?i)NeuralHD`)
	microsoftPrefix = regexp.MustCompile(`(?i)^Microsoft\s+`)
	onlineNatural   = regexp.MustCompile(`(?i)\s*Online\s*\(Natural\)\s*`)
	online          = regexp.MustCompile(`(?i)\s*Online\s*`)
	spaces          = regexp.MustCompile(`\s+`)
)

func replaceFirst(re *regexp.Regexp, s, repl string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + repl + s[loc[1]:]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// normalizeVoices gives every voice the same fields, whatever the service
// filled in, so that DisplayName reads "Aria" and FriendlyName
// "Aria (Neural) - en-US".
func normalizeVoices(raw []rawVoice) []Voice {
	out := make([]Voice, 0, len(raw))
	for _, v := range raw {
		short := v.ShortName

		base := localePrefix.ReplaceAllString(short, "")
		base = neuralHDSuffix.ReplaceAllString(base, "")
		base = strings.TrimSpace(neuralSuffix.ReplaceAllString(base, ""))

		voiceType := v.VoiceType
		if voiceType == "" {
			voiceType = "Neural"
			if neuralHD.MatchString(v.Name + " " + short) {
				voiceType = "NeuralHD"
			}
		}

		localeName := firstNonEmpty(v.LocaleName, v.Locale)

		display := firstNonEmpty(v.DisplayName, v.FriendlyName, base, short)
		display = microsoftPrefix.ReplaceAllString(display, "")
		display, _, _ = strings.Cut(display, " - ")
		display = strings.TrimSpace(display)
		display = replaceFirst(onlineNatural, display, " ")
		display = replaceFirst(online, display, " ")
		display = strings.TrimSpace(spaces.ReplaceAllString(display, " "))

		tailored := v.VoiceTag.TailoredScenarios
		if tailored == nil {
			tailored = v.VoiceTag.ContentCategories
		}

		name := firstNonEmpty(short, v.Name)
		out = append(out, Voice{
			Name:                name,
			DisplayName:         display,
			LocalName:           display,
			ShortName:           name,
			Gender:              v.Gender,
			Locale:              v.Locale,
			LocaleName:          localeName,
			SecondaryLocaleList: orEmpty(v.SecondaryLocaleList),
			VoiceType:           voiceType,
			VoiceTag: VoiceTag{
				TailoredScenarios:  orEmpty(tailored),
				VoicePersonalities: orEmpty(v.VoiceTag.VoicePersonalities),
			},
			FriendlyName: display + " (" + voiceType + ") - " + localeName,
		})
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
