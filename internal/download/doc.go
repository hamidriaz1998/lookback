// Package download fetches pinned files with resume (Range), SHA-256 verification, atomic rename,
// backoff and offline pause. It unpacks runtime archives into the runtimes folder and refuses paths
// that leave it (SPEC section 7).
package download
