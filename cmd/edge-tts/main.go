// Command edge-tts turns text into speech with the Edge text-to-speech service.
//
//	edge-tts synthesize -t "Hello, world!" -o hello
//	edge-tts voice-list
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"time"

	edgetts "github.com/andresayac/edge-tts-go"
)

const usage = `Usage: edge-tts <command> [options]

Commands:
  synthesize   Synthesize text to audio
  voice-list   List the available voices

Options:
  -V, --version   Print the version
  -h, --help      Print this help

Run "edge-tts <command> --help" for the options of a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch cmd := os.Args[1]; cmd {
	case "synthesize":
		err = synthesize(ctx, os.Args[2:])
	case "voice-list":
		err = voiceList(ctx, os.Args[2:])
	case "-V", "--version", "version":
		fmt.Println(version())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func synthesize(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("synthesize", flag.ContinueOnError)
	var text, voice, rate, volume, pitch, output, file string
	both := func(p *string, short, long, value string) {
		fs.StringVar(p, short, value, "")
		fs.StringVar(p, long, value, "")
	}
	both(&text, "t", "text", "")
	both(&voice, "v", "voice", "en-US-AriaNeural")
	both(&rate, "r", "rate", "0%")
	both(&volume, "l", "volume", "0%")
	both(&pitch, "p", "pitch", "0Hz")
	both(&output, "o", "output", fmt.Sprintf("output_%d", time.Now().UnixMilli()))
	both(&file, "f", "file", "")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: edge-tts synthesize [options]

Options:
  -t, --text <text>       Text to convert to audio
  -f, --file <file>       Input file (text)
  -v, --voice <voice>     Voice to use (default "en-US-AriaNeural")
  -r, --rate <rate>       Rate of speech (default "0%")
  -l, --volume <volume>   Volume of speech (default "0%")
  -p, --pitch <pitch>     Pitch of speech (default "0Hz")
  -o, --output <output>   Output file name, without extension (default "output_<timestamp>")
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if text == "" && file == "" {
		return errors.New("text (-t) or file (-f) is required")
	}
	if text != "" && file != "" {
		return errors.New("cannot use both text (-t) and file (-f) options")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		text = string(data)
		fmt.Printf("Reading from file: %s\n", file)
	}

	tts := edgetts.New()
	err := tts.Synthesize(ctx, text, voice,
		edgetts.WithPitch(pitch),
		edgetts.WithRate(rate),
		edgetts.WithVolume(volume),
	)
	if err != nil {
		return err
	}
	path, err := tts.ToFile(output)
	if err != nil {
		return err
	}
	fmt.Printf("Audio file generated: %s\n", path)
	return nil
}

func voiceList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("voice-list", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), "Usage: edge-tts voice-list\n") }
	if err := fs.Parse(args); err != nil {
		return err
	}

	voices, err := edgetts.New().Voices(ctx)
	if err != nil {
		return err
	}
	fmt.Println("Available voices:")
	for _, v := range voices {
		fmt.Printf(" - %s\n", v.ShortName)
	}
	return nil
}
