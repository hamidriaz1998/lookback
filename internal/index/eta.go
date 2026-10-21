package index

import (
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// ewmaAlpha is the weight of each finished item in the moving averages (SPEC section 11, Q27).
const ewmaAlpha = 0.1

// imageTokenEstimate is the cost of an image before it is measured. ponytail: flat estimate; SPEC section 11
// wants pixels times a measured tokens-per-megapixel ratio, add it when the first real runs show the spread.
const imageTokenEstimate = 1500

// ewma is an exponentially weighted moving average. The first sample is the value.
type ewma struct {
	v  float64
	ok bool
}

func (e *ewma) add(sample float64) {
	if !e.ok {
		e.v, e.ok = sample, true
		return
	}
	e.v = (1-ewmaAlpha)*e.v + ewmaAlpha*sample
}

// estimateTokens is the cost of f before it runs: bytes divided by four for text-like files, and a flat
// estimate for images (SPEC section 11, Q27).
func estimateTokens(f contract.FileRec) float64 {
	if f.Kind == contract.KindImage {
		return imageTokenEstimate
	}
	return float64(f.Size) / 4
}

// etaFor returns the time to embed the remaining text and image tokens at the measured rates. A kind with no
// rate yet uses the other kind's rate. ok is false when work remains and neither kind has a rate.
func etaFor(textTokens, imageTokens float64, text, image ewma) (time.Duration, bool) {
	tr, ir := text.v, image.v
	switch {
	case !text.ok && !image.ok:
		if textTokens+imageTokens == 0 {
			return 0, true
		}
		return 0, false
	case !text.ok:
		tr = ir
	case !image.ok:
		ir = tr
	}
	var secs float64
	if textTokens > 0 {
		secs += textTokens / tr
	}
	if imageTokens > 0 {
		secs += imageTokens / ir
	}
	return time.Duration(secs * float64(time.Second)), true
}
