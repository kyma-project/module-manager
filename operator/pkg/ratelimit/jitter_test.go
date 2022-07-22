package ratelimit

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Randomize by ten percent function", func() {
	When("given a positive integer", func() {
		It("should return a value that differs at most 10% from the input", func() {
			var val int = 1000
			res := RandomizeByTenPercent(val)
			diffPercent := math.Abs(float64(res)/float64(val)*100 - 100)
			Expect(diffPercent).Should(BeNumerically("<=", 10.0))

			val = 1000 + int(randomSeed.Int63n(100*1000))
			res = RandomizeByTenPercent(val)
			diffPercent = math.Abs(float64(res)/float64(val)*100 - 100)
			Expect(diffPercent).Should(BeNumerically("<=", 10.0))
		})
	})
})
