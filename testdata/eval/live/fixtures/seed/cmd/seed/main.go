// Command seed prints this environment's deterministic seed.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const label = "qompack-live-seed-v1"

func main() {
	sum := sha256.Sum256([]byte(label))
	fmt.Println("seed:", hex.EncodeToString(sum[:])[:16])
}
