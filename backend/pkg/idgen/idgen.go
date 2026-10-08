// Package idgen generates RFC 4122 v4 UUIDs using only crypto/rand from the
// standard library. A dedicated uuid package (github.com/google/uuid) would
// normally be the idiomatic choice, but it requires `go mod tidy` to reach
// proxy.golang.org — fine on your machine, not reachable in this sandbox.
// Swap this for google/uuid any time; the NewID() signature won't change.
package idgen

import (
	"crypto/rand"
	"fmt"
)

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read failing means the OS RNG is broken — extremely
		// rare, and there's no sane fallback, so this is the one place
		// we panic rather than return a degraded (predictable) ID.
		panic(fmt.Sprintf("idgen: failed to read random bytes: %v", err))
	}
	// set version (4) and variant (RFC 4122) bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
