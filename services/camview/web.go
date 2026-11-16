package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

//go:embed web.html
var page []byte

// camera reads JPEG frames from ffmpeg while anyone watches, and stops a few seconds after the last viewer leaves,
// so the camera is free for other programs the rest of the time.
type camera struct {
	dev     string
	size    string
	fps     int
	quality int
	analyze string // a command that analyses frames (see analyse); "" = off

	mu      sync.Mutex
	cond    *sync.Cond
	frame   []byte // the newest JPEG
	seq     uint64 // counts frames, so a viewer can wait for the next one
	viewers int
	running bool
	err     string
	rate    float64
	idle    *time.Timer
	stop    context.CancelFunc

	// the analyser's newest result (a JSON line) and its numbers
	result []byte
	rseq   uint64
	aon    bool
	ams    float64
	afps   float64
	aerr   string
}

func newCamera(dev, size string, fps, quality int, analyze string) *camera {
	c := &camera{dev: dev, size: size, fps: fps, quality: quality, analyze: analyze}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// join counts a viewer and starts the camera if it is off.
func (c *camera) join() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.viewers++
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	if !c.running {
		ctx, cancel := context.WithCancel(context.Background())
		c.running, c.stop, c.err = true, cancel, ""
		go c.read(ctx)
	}
}

// leave counts a viewer out; the camera stops 5 s after the last one.
func (c *camera) leave() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.viewers--
	if c.viewers == 0 && c.running {
		c.idle = time.AfterFunc(5*time.Second, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.viewers == 0 && c.stop != nil {
				c.stop()
			}
		})
	}
}

func (c *camera) read(ctx context.Context) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "v4l2", "-input_format", "mjpeg",
		"-video_size", c.size, "-framerate", "30", "-i", c.dev,
		"-vf", "fps="+strconv.Itoa(c.fps), "-c:v", "mjpeg", "-q:v", strconv.Itoa(c.quality), "-f", "image2pipe", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err == nil {
		log.Printf("camera on (%s, %s, %d fps)", c.dev, c.size, c.fps)
		if c.analyze != "" {
			go c.analyse(ctx)
		}
		rd := bufio.NewReaderSize(out, 1<<20)
		count, since := 0, time.Now()
		for {
			jpg, err := nextJPEG(rd)
			if err != nil {
				break
			}
			count++
			c.mu.Lock()
			c.frame, c.seq = jpg, c.seq+1
			if d := time.Since(since); d >= time.Second {
				c.rate, count, since = float64(count)/d.Seconds(), 0, time.Now()
			}
			c.cond.Broadcast()
			c.mu.Unlock()
		}
		_ = cmd.Wait()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running, c.rate = false, 0
	if ctx.Err() == nil { // ended by itself: keep the reason for the page
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) == 0 && err != nil {
			msg = []byte(err.Error())
		}
		c.err = string(msg) + " (is another program using the camera?)"
		log.Printf("camera stopped: %s", c.err)
	} else {
		log.Printf("camera off (nobody watching)")
	}
	c.cond.Broadcast()
}

// nextJPEG reads one JPEG from a stream of them: from the start marker to the end marker, walking the segments so an
// end marker inside one (a thumbnail) is not taken for the end.
func nextJPEG(r *bufio.Reader) ([]byte, error) {
	prev := byte(0)
	for { // the start of image: FF D8
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if prev == 0xFF && b == 0xD8 {
			break
		}
		prev = b
	}
	buf := bytes.NewBuffer([]byte{0xFF, 0xD8})
	code, err := marker(r)
	for err == nil {
		buf.Write([]byte{0xFF, code})
		switch {
		case code == 0xD9: // end of image
			return buf.Bytes(), nil
		case code >= 0xD0 && code <= 0xD7, code == 0x01: // markers without a length
			code, err = marker(r)
			continue
		}
		var l [2]byte
		if _, err = io.ReadFull(r, l[:]); err != nil {
			break
		}
		n := int(l[0])<<8 | int(l[1])
		if n < 2 {
			return nil, errors.New("bad JPEG segment length")
		}
		buf.Write(l[:])
		if _, err = io.CopyN(buf, r, int64(n-2)); err != nil {
			break
		}
		if code != 0xDA {
			code, err = marker(r)
			continue
		}
		// start of scan: compressed data up to the next marker; FF 00 is a stuffed FF, FF D0-D7 a restart
		code, err = scan(r, buf)
	}
	return nil, err
}

// marker reads FF (and any FF fill bytes) and returns the marker code after it.
func marker(r *bufio.Reader) (byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	if b != 0xFF {
		return 0, errors.New("not a JPEG marker")
	}
	for b == 0xFF {
		if b, err = r.ReadByte(); err != nil {
			return 0, err
		}
	}
	return b, nil
}

// scan copies compressed data to buf and returns the code of the marker that ends it.
func scan(r *bufio.Reader, buf *bytes.Buffer) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != 0xFF {
			buf.WriteByte(b)
			continue
		}
		next := byte(0xFF)
		for next == 0xFF {
			if next, err = r.ReadByte(); err != nil {
				return 0, err
			}
		}
		if next == 0x00 || (next >= 0xD0 && next <= 0xD7) {
			buf.Write([]byte{0xFF, next})
			continue
		}
		return next, nil
	}
}

func serve(addr, dev, size string, fps, quality int, analyze string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		fmt.Fprintf(os.Stderr, "camview: %s is reachable from other machines: anyone who can reach it sees the camera\n", addr)
	}
	cam := newCamera(dev, size, fps, quality, analyze)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self' blob:")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		cam.join()
		defer cam.leave()
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-store")
		flusher, _ := w.(http.Flusher)
		var seen uint64
		done := r.Context().Done()
		go func() { // wake the wait below when the viewer goes away
			<-done
			cam.mu.Lock()
			cam.cond.Broadcast()
			cam.mu.Unlock()
		}()
		for {
			cam.mu.Lock()
			for cam.seq == seen && cam.running && r.Context().Err() == nil {
				cam.cond.Wait()
			}
			jpg, seq, running := cam.frame, cam.seq, cam.running
			cam.mu.Unlock()
			if r.Context().Err() != nil || (!running && seq == seen) {
				return
			}
			seen = seq
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(jpg)); err != nil {
				return
			}
			if _, err := w.Write(append(jpg, '\r', '\n')); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("GET /analysis", cam.analysisEvents)
	mux.HandleFunc("GET /snapshot.jpg", func(w http.ResponseWriter, _ *http.Request) {
		cam.mu.Lock()
		jpg := cam.frame
		cam.mu.Unlock()
		if jpg == nil {
			http.Error(w, "no picture yet: open the page first", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Disposition", "attachment; filename=\"camera-"+time.Now().Format("20060102-150405")+".jpg\"")
		_, _ = w.Write(jpg)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		cam.mu.Lock()
		s := map[string]any{"device": cam.dev, "size": cam.size, "running": cam.running, "viewers": cam.viewers,
			"fps": float64(int(cam.rate*10)) / 10, "bytes": len(cam.frame), "error": cam.err,
			"analysis": map[string]any{"configured": cam.analyze != "", "on": cam.aon, "ms": cam.ams, "fps": float64(int(cam.afps*10)) / 10, "error": cam.aerr}}
		cam.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("camview: http://%s/ (Ctrl-C stops)", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
