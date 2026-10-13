package edgetts

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// frame is a message the service sent in the recorded session.
type frame struct {
	Text   *string `json:"text"`
	Binary string  `json:"binary"`
}

type session struct {
	Text   string  `json:"text"`
	Frames []frame `json:"frames"`
}

func loadSession(t *testing.T) session {
	t.Helper()
	data, err := os.ReadFile("testdata/session.json")
	if err != nil {
		t.Fatal(err)
	}
	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// recordedAudio returns the payload of every binary frame, in order.
func recordedAudio(t *testing.T, frames []frame) []byte {
	t.Helper()
	var out []byte
	for _, f := range frames {
		if f.Text != nil {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(f.Binary)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b[2+int(binary.BigEndian.Uint16(b)):]...)
	}
	return out
}

type connection struct {
	url      *url.URL
	headers  http.Header
	messages []string
}

type fakeService struct {
	// Status to answer the handshake with instead of upgrading.
	handshakeStatus int
	// Value of the Date header on every response.
	date func() time.Time
	// Refuses the handshake unless this returns true for the request URL.
	accept func(*url.URL) bool
	// Frames to replay after the SSML message.
	frames []frame
	// Does not answer the SSML message at all.
	silent      bool
	voiceStatus int
	voiceBody   string

	mu            sync.Mutex
	connections   []*connection
	voiceRequests []*http.Request
	server        *httptest.Server
}

func (f *fakeService) conn(i int) *connection {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connections[i]
}

func (f *fakeService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	date := time.Now()
	if f.date != nil {
		date = f.date()
	}
	w.Header().Set("Date", date.UTC().Format(http.TimeFormat))

	switch r.URL.Path {
	case "/voices":
		f.mu.Lock()
		f.voiceRequests = append(f.voiceRequests, r)
		f.mu.Unlock()
		status := f.voiceStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		w.Write([]byte(f.voiceBody))
	case "/ws":
		status := f.handshakeStatus
		if status == 0 && f.accept != nil && !f.accept(r.URL) {
			status = http.StatusForbidden
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		// The client sends the Origin of the Edge extension.
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		c := &connection{url: r.URL, headers: r.Header}
		f.mu.Lock()
		f.connections = append(f.connections, c)
		f.mu.Unlock()
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			msg := string(data)
			f.mu.Lock()
			c.messages = append(c.messages, msg)
			f.mu.Unlock()
			if !strings.Contains(msg, "Path:ssml") || f.silent {
				continue
			}
			for _, fr := range f.frames {
				if fr.Text != nil {
					ws.WriteMessage(websocket.TextMessage, []byte(*fr.Text))
					continue
				}
				b, _ := base64.StdEncoding.DecodeString(fr.Binary)
				ws.WriteMessage(websocket.BinaryMessage, b)
			}
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// startService serves the recorded session unless the service says
// otherwise, and returns a client that talks to it.
func startService(t *testing.T, f *fakeService) (*fakeService, *EdgeTTS) {
	t.Helper()
	if f.frames == nil {
		f.frames = loadSession(t).Frames
	}
	f.server = httptest.NewServer(f)
	t.Cleanup(f.server.Close)
	t.Cleanup(func() { clockSkew.Store(0) })

	tts := New()
	tts.wssURL = "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws"
	tts.voicesURL = f.server.URL + "/voices"
	return f, tts
}
