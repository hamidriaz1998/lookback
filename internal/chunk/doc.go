// Package chunk splits text into token-exact windows of 512 tokens with a 64-token overlap. It takes a
// tokenizer function, so its tests run without the sidecar. It maps each window back to character
// offsets (SPEC section 9).
package chunk
