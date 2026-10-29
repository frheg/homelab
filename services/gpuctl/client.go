package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// refused is an answer from gpud that is not a failure: busy or reserved.
type refused struct{ msg string }

func (e refused) Error() string { return e.msg }

// call sends one request to gpud and returns the raw JSON reply.
func call(addr, method, path string, body any) ([]byte, error) {
	var in io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		in = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, addr+path, in)
	if err != nil {
		return nil, err
	}
	// acquire blocks while the GPU switches
	c := &http.Client{Timeout: 30 * time.Minute}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		return out, nil
	}
	var e struct{ Error string }
	json.Unmarshal(out, &e)
	if e.Error == "" {
		e.Error = resp.Status
	}
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusLocked {
		return nil, refused{e.Error}
	}
	return nil, fmt.Errorf("%s", e.Error)
}

func printState(raw []byte, asJSON bool) {
	if asJSON {
		fmt.Println(string(raw))
		return
	}
	var s State
	json.Unmarshal(raw, &s)
	fmt.Printf("holder:   %s\n", orDash(s.Holder))
	fmt.Printf("since:    %s\n", ts(s.Since))
	fmt.Printf("lease:    %s\n", ts(s.LeaseUntil))
	fmt.Printf("reserved: %s\n", orDash(s.ReservedBy))
	if s.ReservedBy != "" {
		fmt.Printf("until:    %s\n", ts(s.ReservedUntil))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ts(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
