// Package chunk splits text into token-exact windows of 512 tokens with a 64-token overlap. It takes a
// tokenizer function, so its tests run without the sidecar. Window offsets are byte offsets into the UTF-8
// text (SPEC section 9). Cuts move to character starts, so every chunk is valid UTF-8. The full prompt,
// with its prefix, is tokenized again to enforce the 512-token cap, and a window that is over shrinks.
package chunk
