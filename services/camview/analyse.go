package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// analyse runs the analyser (services/pose) while the camera is on: it gets the newest frame (a 4-byte length, then
// the JPEG) whenever it is ready for one, so it never falls behind, and answers each with a JSON line.
func (c *camera) analyse(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "sh", "-c", c.analyze)
	tail := &lastLines{}
	cmd.Stderr = tail
	in, err := cmd.StdinPipe()
	if err != nil {
		c.analysisFailed(ctx, err.Error())
		return
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		c.analysisFailed(ctx, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		c.analysisFailed(ctx, err.Error())
		return
	}
	defer func() { _ = cmd.Wait() }()
	defer in.Close()
	rd := bufio.NewReaderSize(out, 1<<20)
	if _, err := rd.ReadBytes('\n'); err != nil { // the analyser says it is ready once its models are loaded
		c.analysisFailed(ctx, "the analyser did not start: "+tail.String())
		return
	}
	c.mu.Lock()
	c.aon, c.aerr = true, ""
	c.mu.Unlock()
	log.Printf("analysis on")
	var seen uint64
	count, since := 0, time.Now()
	head := make([]byte, 4)
	for {
		c.mu.Lock()
		for c.seq == seen && c.running && ctx.Err() == nil {
			c.cond.Wait()
		}
		jpg, seq, ok := c.frame, c.seq, c.running && ctx.Err() == nil
		c.mu.Unlock()
		if !ok {
			break
		}
		seen = seq
		binary.BigEndian.PutUint32(head, uint32(len(jpg)))
		if _, err := in.Write(append(head, jpg...)); err != nil {
			c.analysisFailed(ctx, "the analyser stopped: "+tail.String())
			return
		}
		line, err := rd.ReadBytes('\n')
		if err != nil {
			c.analysisFailed(ctx, "the analyser stopped: "+tail.String())
			return
		}
		var r struct {
			MS float64 `json:"ms"`
		}
		_ = json.Unmarshal(line, &r)
		count++
		c.mu.Lock()
		c.result, c.rseq, c.ams = line, c.rseq+1, r.MS
		if d := time.Since(since); d >= time.Second {
			c.afps, count, since = float64(count)/d.Seconds(), 0, time.Now()
		}
		c.cond.Broadcast()
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.aon, c.afps, c.result = false, 0, nil
	c.cond.Broadcast()
	c.mu.Unlock()
	log.Printf("analysis off")
}

func (c *camera) analysisFailed(ctx context.Context, msg string) {
	if ctx.Err() != nil {
		msg = "" // stopped because the camera stopped
	} else {
		log.Printf("analysis: %s", msg)
	}
	c.mu.Lock()
	c.aon, c.afps, c.aerr, c.result = false, 0, msg, nil
	c.cond.Broadcast()
	c.mu.Unlock()
}

// lastLines keeps the end of what a process writes to stderr, for error messages.
type lastLines struct {
	mu  sync.Mutex
	buf []byte
}

func (l *lastLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > 2048 {
		l.buf = l.buf[len(l.buf)-2048:]
	}
	return len(p), nil
}

func (l *lastLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

// analysisEvents streams the analyser's results to the page as Server-Sent Events, one per analysed frame.
func (c *camera) analysisEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	done := r.Context().Done()
	go func() {
		<-done
		c.mu.Lock()
		c.cond.Broadcast()
		c.mu.Unlock()
	}()
	var seen uint64
	for {
		c.mu.Lock()
		for c.rseq == seen && r.Context().Err() == nil {
			c.cond.Wait()
		}
		line, seq := c.result, c.rseq
		c.mu.Unlock()
		if r.Context().Err() != nil {
			return
		}
		seen = seq
		if line == nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", line[:len(line)-1]); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
