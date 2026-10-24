package main

import "testing"

// The firstboot screen must differ from the background once a stage is drawn.
func TestRenderScreenStage(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		c := newCanvas(screenW, screenH)
		renderScreenStage(c, screenStageTexts[lang]["download"], 42, lang)
		b0, b1 := c.Buf[0], c.Buf[1]
		diff := 0
		for i := 0; i+1 < len(c.Buf); i += 2 {
			if c.Buf[i] != b0 || c.Buf[i+1] != b1 {
				diff++
			}
		}
		if diff < 200 {
			t.Fatalf("%s stage rendered only %d non-background pixels", lang, diff)
		}
	}
}
