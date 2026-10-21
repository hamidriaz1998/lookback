// Package bench runs the hardware self-test and the combination test lab. It measures image latency,
// text tokens per second, peak RAM, VRAM and quality against the BF16 references. It recommends the
// fastest combination whose quality is at least 0.99 (SPEC section 6).
package bench
