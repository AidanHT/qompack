// Command code prints this checkout's code word.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const label = "qompack-live-pilot-v1"

func main() {
	sum := sha256.Sum256([]byte(label))
	fmt.Println("code:", hex.EncodeToString(sum[:])[:10])
}
