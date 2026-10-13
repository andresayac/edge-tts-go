// Stream writes the audio to a file as it arrives from the service.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	edgetts "github.com/andresayac/edge-tts-go"
)

func main() {
	out, err := os.Create("stream.mp3")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	tts := edgetts.New()
	text := "Streaming lets you play the first words before the last ones are synthesized."
	total := 0
	for chunk, err := range tts.Stream(context.Background(), text, "en-US-AriaNeural") {
		if err != nil {
			log.Fatal(err)
		}
		if _, err := out.Write(chunk); err != nil {
			log.Fatal(err)
		}
		total += len(chunk)
		fmt.Printf("received %d bytes\n", len(chunk))
	}
	fmt.Printf("wrote %d bytes to stream.mp3\n", total)
}
