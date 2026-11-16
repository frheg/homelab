package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestFit(t *testing.T) {
	// a wide terminal is limited by its height, a narrow one by its width; cells are twice as tall as wide
	if w, h := fit(300, 50, 16.0/9); h != 50 || w != 178 {
		t.Fatal(w, h)
	}
	if w, h := fit(80, 50, 16.0/9); w != 80 || h != 23 {
		t.Fatal(w, h)
	}
	if w, h := fit(0, 0, 16.0/9); w != 1 || h != 1 {
		t.Fatal("never zero:", w, h)
	}
}

func TestRender(t *testing.T) {
	// one row: black, mid grey, white
	frame := []byte{0, 0, 0, 128, 128, 128, 255, 255, 255}
	var b bytes.Buffer
	render(&b, frame, 3, 1, 2, options{ramp: []rune(ramps["simple"])})
	if got := b.String(); got != "\x1b[1;3H =@" {
		t.Fatalf("%q", got)
	}
	b.Reset()
	render(&b, frame, 3, 1, 0, options{ramp: []rune(ramps["simple"]), invert: true})
	if !strings.HasSuffix(b.String(), "@= ") {
		t.Fatalf("inverted: %q", b.String())
	}
	b.Reset()
	render(&b, []byte{200, 10, 10, 200, 10, 10, 10, 10, 200}, 3, 1, 0, options{ramp: []rune(ramps["blocks"]), color: true})
	if s := b.String(); strings.Count(s, "\x1b[38;2;") != 2 || !strings.HasSuffix(s, "\x1b[0m") {
		t.Fatalf("a colour code only when the colour changes: %q", s)
	}
}

func TestNextJPEG(t *testing.T) {
	// two tiny frames back to back; the first has stuffed FF 00 and a restart marker in its scan, and an APP1
	// segment holding FF D9, which must not be taken for the end
	a := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x06, 0xFF, 0xD9, 0x01, 0x02, 0xFF, 0xDA, 0x00, 0x02, 0x11, 0xFF, 0x00, 0x22, 0xFF, 0xD0, 0x33, 0xFF, 0xD9}
	b := []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02, 0x44, 0xFF, 0xD9}
	r := bufio.NewReader(bytes.NewReader(append(append([]byte{0x00, 0x13}, a...), b...)))
	got, err := nextJPEG(r)
	if err != nil || !bytes.Equal(got, a) {
		t.Fatalf("first: %x %v", got, err)
	}
	if got, err := nextJPEG(r); err != nil || !bytes.Equal(got, b) {
		t.Fatalf("second: %x %v", got, err)
	}
	if _, err := nextJPEG(r); err == nil {
		t.Fatal("the stream has ended")
	}
}
