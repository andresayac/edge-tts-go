package edgetts_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	edgetts "github.com/andresayac/edge-tts-go"
)

// These examples talk to the real service, so they are compiled but not run.

func Example() {
	tts := edgetts.New()
	if err := tts.Synthesize(context.Background(), "Hello, world!", "en-US-AriaNeural"); err != nil {
		log.Fatal(err)
	}
	path, err := tts.ToFile("hello")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("saved", path) // saved hello.mp3
}

func ExampleEdgeTTS_Synthesize() {
	tts := edgetts.New()
	err := tts.Synthesize(context.Background(), "Hola, ¿cómo estás?", "es-CO-SalomeNeural",
		edgetts.WithRate("+10%"),
		edgetts.WithVolume("-5%"),
		edgetts.WithPitch("-2Hz"),
		edgetts.WithOutputFormat(edgetts.Audio24khz96kbitrateMonoMP3),
	)
	if errors.Is(err, edgetts.ErrEmptyAudio) {
		log.Fatal("the voice cannot read this text, pick one of its language")
	}
	if err != nil {
		log.Fatal(err)
	}
	audio, _ := tts.Bytes()
	fmt.Println(len(audio), "bytes")
}

func ExampleEdgeTTS_Stream() {
	// Sends the audio to the browser as the service produces it.
	http.HandleFunc("/speak", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		tts := edgetts.New()
		for chunk, err := range tts.Stream(r.Context(), r.URL.Query().Get("text"), "en-US-AriaNeural") {
			if err != nil {
				log.Print(err) // the headers are gone, the client sees a cut stream
				return
			}
			w.Write(chunk)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func ExampleEdgeTTS_VoicesByLanguage() {
	voices, err := edgetts.New().VoicesByLanguage(context.Background(), "es-CO")
	if err != nil {
		log.Fatal(err)
	}
	for _, v := range voices {
		fmt.Println(v.ShortName, v.Gender) // es-CO-GonzaloNeural Male ...
	}
}

func ExampleEdgeTTS_Generate() {
	// One client, many goroutines: Generate keeps nothing in the EdgeTTS.
	tts := edgetts.New(edgetts.WithTimeout(20 * time.Second))
	r, err := tts.Generate(context.Background(), "¿Cómo estás? ¡Qué bien!", "es-CO-SalomeNeural")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := r.ToFile("greeting"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(r.SRT())
}

func ExampleEdgeTTS_WordBoundaries() {
	tts := edgetts.New()
	if err := tts.Synthesize(context.Background(), "Hello, world!", "en-US-AriaNeural"); err != nil {
		log.Fatal(err)
	}
	for _, w := range tts.WordBoundaries() {
		fmt.Printf("%s - %s  %s\n", w.Start(), w.End(), w.Text)
	}
}
