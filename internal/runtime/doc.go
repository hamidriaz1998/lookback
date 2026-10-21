// Package runtime runs the llama-server sidecar for the active combination: start on a free port
// with an API key, health checks, logs, restart and CPU fallback, and process cleanup. Its client
// implements contract.Embedder (SPEC section 8).
package runtime
