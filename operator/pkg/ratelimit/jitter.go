package ratelimit

import (
	"math/rand"
	"time"
)

//nolint:gosec,gochecknoglobals
var RandomSeed = rand.New(rand.NewSource(time.Now().UnixNano()))

const (
	tenPercentMultiplier = 0.1
)

// RandomizeByTenPercent returns the value that is randomly changed by at most +/- 10% from the input.
// May return the same value.
func RandomizeByTenPercent(val int) int {
	floatValue := float64(val)

	tenPercentOfVal := tenPercentMultiplier * floatValue

	randomFactor := 1 - RandomSeed.Float64() // random number in range [-1 ... 0 ... +1)
	delta := randomFactor * tenPercentOfVal  // +- 10%

	return int(floatValue + delta)

	// TODO: replace with crypto/rand
	// timeVal := big.NewInt(time.Now().UnixNano())
	// randomSeed, err := rand.Int(rand.Reader, timeVal)
	// if err != nil {
	//	return 0, err
	//}
	//
	// return randomSeed.Int64(), nil
}
