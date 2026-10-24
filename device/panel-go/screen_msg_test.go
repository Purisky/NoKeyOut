package main

import "testing"

// The firstboot screen must differ from the background once a message is drawn.
func TestRenderScreenMsg(t *testing.T) {
	c := newCanvas(screenW, screenH)
	renderScreenMsg(c, "Installing NoKeyOut", "Configuring panel", 70)
	b0, b1 := c.Buf[0], c.Buf[1]
	diff := 0
	for i := 0; i+1 < len(c.Buf); i += 2 {
		if c.Buf[i] != b0 || c.Buf[i+1] != b1 {
			diff++
		}
	}
	if diff < 200 {
		t.Fatalf("screen message rendered only %d non-background pixels", diff)
	}
}
