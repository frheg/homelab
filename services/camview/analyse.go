package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// analyser finds pose, hands and gestures in one JPEG and answers with one JSON line (the format of Athena's pose service).
type analyser interface {
	analyse(ctx context.Context, jpg []byte) ([]byte, error)
	close()
}

// analyse runs the analysis while the camera is on: it takes the newest frame whenever the analyser is ready for
// one, so it never falls behind. c.analyze is either a URL (Athena's POST /api/camera: the models run in the
// cluster) or a command (a 4-byte length and the JPEG on its stdin, a JSON line per frame on its stdout).
func (c *camera) analyse(ctx context.Context) {
	var a analyser
	var err error
	if strings.HasPrefix(c.analyze, "http://") || strings.HasPrefix(c.analyze, "https://") {
		a = newRemote(c.analyze)
	} else if a, err = startCommand(ctx, c.analyze); err != nil {
		c.analysisFailed(ctx, err.Error())
		return
	}
	defer a.close()
	c.mu.Lock()
	c.aon, c.aerr = true, ""
	c.mu.Unlock()
	log.Printf("analysis on")
	var seen uint64
	count, since := 0, time.Now()
	failures := 0
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
		line, err := a.analyse(ctx, jpg)
		if err != nil {
			if _, isCmd := a.(*command); isCmd || ctx.Err() != nil {
				c.analysisFailed(ctx, err.Error())
				return
			}
			// a remote analyser can be away for a moment (a restart): wait and go on
			failures++
			c.mu.Lock()
			c.aerr = err.Error()
			c.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(min(time.Duration(failures)*time.Second, 10*time.Second)):
			}
			continue
		}
		failures = 0
		var r struct {
			MS float64 `json:"ms"`
		}
		_ = json.Unmarshal(line, &r)
		count++
		c.mu.Lock()
		c.result, c.rseq, c.ams, c.aerr = line, c.rseq+1, r.MS, ""
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

// command is an analyser run as a process.
type command struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	rd   *bufio.Reader
	tail *lastLines
}

func startCommand(ctx context.Context, line string) (*command, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	tail := &lastLines{}
	cmd.Stderr = tail
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &command{cmd: cmd, in: in, rd: bufio.NewReaderSize(out, 1<<20), tail: tail}
	if _, err := c.rd.ReadBytes('\n'); err != nil { // the analyser says it is ready once its models are loaded
		c.close()
		return nil, fmt.Errorf("the analyser did not start: %s", tail)
	}
	return c, nil
}

func (c *command) analyse(_ context.Context, jpg []byte) ([]byte, error) {
	head := make([]byte, 4)
	binary.BigEndian.PutUint32(head, uint32(len(jpg)))
	if _, err := c.in.Write(append(head, jpg...)); err != nil {
		return nil, fmt.Errorf("the analyser stopped: %s", c.tail)
	}
	line, err := c.rd.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("the analyser stopped: %s", c.tail)
	}
	return line, nil
}

func (c *command) close() {
	c.in.Close()
	_ = c.cmd.Wait()
}

// remote is Athena's pose service, through POST /api/camera. It signs in like Athena's other clients: ATHENA_TOKEN,
// or ATHENA_USER and ATHENA_PASSWORD (basic sign-in at the gate).
type remote struct {
	url  string
	hc   *http.Client
	user string
	pass string
	tok  string
}

func newRemote(url string) *remote {
	return &remote{url: url, hc: &http.Client{Timeout: 10 * time.Second},
		user: os.Getenv("ATHENA_USER"), pass: os.Getenv("ATHENA_PASSWORD"), tok: os.Getenv("ATHENA_TOKEN")}
}

func (r *remote) analyse(ctx context.Context, jpg []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(jpg))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "image/jpeg")
	if r.tok != "" {
		req.Header.Set("Authorization", "Bearer "+r.tok)
	} else if r.user != "" {
		req.SetBasicAuth(r.user, r.pass)
	}
	res, err := r.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("analysis: HTTP %d %s", res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 200)])))
	}
	b = bytes.TrimSpace(b)
	if !json.Valid(b) {
		return nil, fmt.Errorf("analysis: the answer is not JSON")
	}
	return append(b, '\n'), nil
}

func (r *remote) close() {}

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
