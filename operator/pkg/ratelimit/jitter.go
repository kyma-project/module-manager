package ratelimit

import (
	"math/rand"
	"time"
)

var randomSeed = rand.New(rand.NewSource(time.Now().UnixNano()))

// RandomizeByTenPercent returns the value that is randomly changed by at most +/- 10% from the input.
// May return the same value.
func RandomizeByTenPercent(val int) int {
	fv := float64(val)

	var tenPercentOfVal float64 = 0.1 * fv

	randomFactor := 1 - randomSeed.Float64()*2 // random number in range [-1 ... 0 ... +1)
	delta := randomFactor * tenPercentOfVal    // +- 10%

	return int(fv + delta)
}
