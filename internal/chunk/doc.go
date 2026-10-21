// Package chunk splits text into windows of at most 512 tokens, with an overlap of at least 64 tokens.
// It takes a tokenizer function, so its tests run without the sidecar. Window offsets are byte offsets
// into the UTF-8 text (SPEC section 9). Window ends and starts move to character starts, so every chunk
// is valid UTF-8, and each window advances by at least one character. The full prompt, with its prefix,
// is tokenized again to enforce the 512-token cap; a window that is over shrinks and is checked again.
package chunk
