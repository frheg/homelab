// camview shows a webcam in the terminal as ASCII art, live.
//
//	camview [-d /dev/video0] [-mono] [-ramp detailed|simple|blocks] [-invert] [-no-mirror] [-fps 15]
//
// ffmpeg reads the camera and scales each frame to the terminal; camview turns each pixel into a character by its
// brightness and, unless -mono, colours it (24-bit colour). q, Esc or Ctrl-C quits; the picture follows the window
// size. The camera is only read while camview runs, and nothing is saved.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// ffmpeg is the ffmpeg to run; the nix build sets it to its own (-X main.ffmpeg=...).
var ffmpeg = "ffmpeg"

var ramps = map[string]string{
	"detailed": " .'`^\",:;Il!i><~+_-?][}{1)(|\\/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$",
	"simple":   " .:-=+*#%@",
	"blocks":   " ░▒▓█",
}

type options struct {
	ramp   []rune
	color  bool
	invert bool
}

// fit returns the picture size in characters for a camera of aspect width/height in a terminal of cols x rows. A
// character cell is about twice as tall as it is wide.
func fit(cols, rows int, aspect float64) (w, h int) {
	w = cols
	h = int(float64(cols)/aspect/2 + 0.5)
	if h > rows {
		h = rows
		w = int(float64(rows)*aspect*2 + 0.5)
	}
	return max(w, 1), max(h, 1)
}

// render turns one RGB frame (w x h, 3 bytes a pixel) into terminal text: a line per row, each starting at column
// pad+1. Colour codes are only written when the colour changes.
func render(out *bytes.Buffer, frame []byte, w, h, pad int, o options) {
	n := len(o.ramp) - 1
	for y := 0; y < h; y++ {
		fmt.Fprintf(out, "\x1b[%d;%dH", y+1, pad+1)
		last := -1
		for x := 0; x < w; x++ {
			p := frame[(y*w+x)*3:]
			r, g, b := int(p[0]), int(p[1]), int(p[2])
			lum := (2126*r + 7152*g + 722*b) / 10000
			if o.invert {
				lum = 255 - lum
			}
			if o.color {
				// 5 bits a channel is plenty in a terminal and keeps the output small
				c := (r>>3)<<10 | (g>>3)<<5 | b>>3
				if c != last {
					fmt.Fprintf(out, "\x1b[38;2;%d;%d;%dm", r&^7, g&^7, b&^7)
					last = c
				}
			}
			out.WriteRune(o.ramp[lum*n/255])
		}
		if o.color {
			out.WriteString("\x1b[0m")
		}
	}
}

func main() {
	dev := flag.String("d", "/dev/video0", "camera device")
	mono := flag.Bool("mono", false, "no colour, characters only")
	ramp := flag.String("ramp", "detailed", "characters by brightness: detailed, simple or blocks")
	invert := flag.Bool("invert", false, "dark characters for bright light (for light terminal themes)")
	noMirror := flag.Bool("no-mirror", false, "show the picture as the camera sees it, not mirrored")
	fps := flag.Int("fps", 15, "frames a second")
	flag.Parse()
	r, ok := ramps[*ramp]
	if !ok {
		fmt.Fprintln(os.Stderr, "camview: -ramp must be detailed, simple or blocks")
		os.Exit(2)
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "camview: run it in a terminal")
		os.Exit(2)
	}
	o := options{ramp: []rune(r), color: !*mono, invert: *invert}
	if err := run(*dev, o, !*noMirror, *fps); err != nil {
		fmt.Fprintln(os.Stderr, "camview:", err)
		os.Exit(1)
	}
}

func run(dev string, o options, mirror bool, fps int) error {
	in := int(os.Stdin.Fd())
	old, err := term.MakeRaw(in)
	if err != nil {
		return err
	}
	// alternate screen, no cursor; both undone on the way out
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[2J")
	defer func() {
		fmt.Print("\x1b[0m\x1b[?25h\x1b[?1049l")
		_ = term.Restore(in, old)
	}()

	quit := make(chan struct{})
	go func() { // q, Esc or Ctrl-C
		b := make([]byte, 1)
		for {
			if n, err := os.Stdin.Read(b); err != nil || (n == 1 && (b[0] == 'q' || b[0] == 27 || b[0] == 3)) {
				close(quit)
				return
			}
		}
	}()
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)

	for {
		cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			return err
		}
		w, h := fit(cols, rows-1, 16.0/9) // the last row is the status line
		pad := (cols - w) / 2
		vf := fmt.Sprintf("fps=%d,scale=%d:%d:flags=area,format=rgb24", fps, w, h)
		if mirror {
			vf = "hflip," + vf
		}
		cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "v4l2", "-input_format", "mjpeg",
			"-video_size", "1280x720", "-framerate", "30", "-i", dev, "-vf", vf, "-f", "rawvideo", "-")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting ffmpeg: %w", err)
		}
		frames := make(chan error, 1)
		go func() { frames <- show(pipe, w, h, pad, rows, o, dev) }()
		fmt.Print("\x1b[2J")

		var why error
		select {
		case <-quit:
		case <-stop:
		case <-resized:
			why = errResize
		case err := <-frames:
			why = err
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if why == errResize {
			continue
		}
		if why != nil && !errors.Is(why, io.EOF) {
			return why
		}
		if why != nil { // ffmpeg ended on its own: say why
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "ffmpeg stopped"
			}
			return errors.New(msg + " (is another program using the camera?)")
		}
		return nil
	}
}

var errResize = errors.New("resized")

// show reads frames and draws them until the stream ends.
func show(pipe io.Reader, w, h, pad, rows int, o options, dev string) error {
	rd := bufio.NewReaderSize(pipe, w*h*3)
	frame := make([]byte, w*h*3)
	out := bufio.NewWriterSize(os.Stdout, 1<<20)
	var buf bytes.Buffer
	count, since, rate := 0, time.Now(), 0.0
	for {
		if _, err := io.ReadFull(rd, frame); err != nil {
			return err
		}
		buf.Reset()
		render(&buf, frame, w, h, pad, o)
		count++
		if d := time.Since(since); d >= time.Second {
			rate, count, since = float64(count)/d.Seconds(), 0, time.Now()
		}
		status := fmt.Sprintf(" camview  %s  %dx%d  %s fps  q quits ", dev, w, h, strconv.FormatFloat(rate, 'f', 1, 64))
		fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K\x1b[7m%s\x1b[0m", rows, status)
		_, _ = out.Write(buf.Bytes())
		if err := out.Flush(); err != nil {
			return err
		}
	}
}
