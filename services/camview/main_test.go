package main

import (
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
