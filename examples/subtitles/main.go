// Subtitles generates the audio and its subtitles in one call.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	edgetts "github.com/andresayac/edge-tts-go"
)

func main() {
	r, err := edgetts.New().Generate(context.Background(),
		"¿Cómo estás? ¡Qué bien! Esto es una prueba de subtítulos.", "es-CO-SalomeNeural")
	if err != nil {
		log.Fatal(err)
	}

	path, err := r.ToFile("subtitles")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("subtitles.srt", []byte(r.SRT()), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("subtitles.vtt", []byte(r.VTT()), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Saved %s, about %s\n\n%s", path, r.Duration.Round(100_000_000), r.SRT())
}
