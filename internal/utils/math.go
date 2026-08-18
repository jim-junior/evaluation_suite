package utils

import (
	"sort"
)

// interquartileRange returns the lower (Q1) and upper (Q3)
// boundaries of the middle 50% of the data.
func InterquartileRange(values []float64) (float64, float64) {
	if len(values) < 2 {
		return 0, 0
	}

	data := append([]float64(nil), values...)
	sort.Float64s(data)

	median := func(values []float64) float64 {
		n := len(values)
		mid := n / 2

		if n%2 == 0 {
			return (values[mid-1] + values[mid]) / 2
		}

		return values[mid]
	}

	mid := len(data) / 2

	lower := data[:mid]
	upper := data[(len(data)+1)/2:]

	q1 := median(lower)
	q3 := median(upper)

	return q1, q3
}

// mean calculates the arithmetic mean.
func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64

	for _, value := range values {
		sum += value
	}

	return sum / float64(len(values))
}
