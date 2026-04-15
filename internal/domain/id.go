package domain

import (
	"crypto/rand"
	"encoding/hex"
)

func NewID(prefix string) string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return prefix + "_" + hex.EncodeToString(buf)
}
