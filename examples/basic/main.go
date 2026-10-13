// Basic synthesizes a sentence to a file and lists the Spanish voices.
package main

import (
	"context"
	"fmt"
	"log"

	edgetts "github.com/andresayac/edge-tts-go"
)

func main() {
	ctx := context.Background()
	tts := edgetts.New()

	voices, err := tts.VoicesByLanguage(ctx, "es")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d Spanish voices\n", len(voices))
	for _, v := range voices[:min(5, len(voices))] {
		fmt.Printf(" - %s (%s)\n", v.ShortName, v.FriendlyName)
	}

	err = tts.Synthesize(ctx, "Hola, ¿cómo estás? Esto es una prueba.", "es-CO-SalomeNeural",
		edgetts.WithRate("+10%"),
		edgetts.WithPitch("-5Hz"),
		edgetts.WithOutputFormat(edgetts.Audio24khz96kbitrateMonoMP3),
	)
	if err != nil {
		log.Fatal(err)
	}

	path, err := tts.ToFile("hola")
	if err != nil {
		log.Fatal(err)
	}
	info, _ := tts.AudioInfo()
	fmt.Printf("Saved %s: %d bytes, about %s\n", path, info.Size, info.EstimatedDuration)

	for _, w := range tts.WordBoundaries() {
		fmt.Printf("%8s  %s\n", w.Start(), w.Text)
	}
}
