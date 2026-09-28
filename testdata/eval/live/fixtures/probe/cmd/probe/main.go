// Command probe prints the build token of this checkout.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const seed = "qompack-live-probe-v1"

func main() {
	sum := sha256.Sum256([]byte(seed))
	fmt.Println("build-token:", hex.EncodeToString(sum[:])[:12])
}
