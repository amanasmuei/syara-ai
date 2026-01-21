// Package crawler provides shared utilities for web crawlers.
package crawler

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// hashContent generates a SHA256 hash of the content.
func hashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// readAll reads all data from a reader.
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
