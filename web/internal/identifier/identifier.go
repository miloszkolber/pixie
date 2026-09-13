package identifier

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a random UUIDv4 string and panics only if the operating system's
// cryptographic random source is unavailable.
func New() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexID := hex.EncodeToString(value)
	return hexID[0:8] + "-" + hexID[8:12] + "-" + hexID[12:16] + "-" + hexID[16:20] + "-" + hexID[20:]
}
