package utils_test

import (
	"testing"

	"github.com/urunc-dev/evaluation_suite/internal/utils"
)

func TestInterquartileRange(t *testing.T) {
	tests := []struct {
		name          string
		values        []float64
		expectedLower float64
		expectedUpper float64
	}{
		{
			name:          "even number of values",
			values:        []float64{1, 2, 3, 4, 5, 6, 7, 8},
			expectedLower: 2.5,
			expectedUpper: 6.5,
		},
		{
			name:          "odd number of values",
			values:        []float64{1, 2, 3, 4, 5, 6, 7},
			expectedLower: 2,
			expectedUpper: 6,
		},
		{
			name:          "unsorted values",
			values:        []float64{8, 3, 1, 7, 2, 6, 4, 5},
			expectedLower: 2.5,
			expectedUpper: 6.5,
		},
		{
			name:          "values with decimals",
			values:        []float64{1.5, 2.5, 3.5, 4.5, 5.5, 6.5},
			expectedLower: 2.5,
			expectedUpper: 5.5,
		},
		{
			name:          "two values",
			values:        []float64{10, 20},
			expectedLower: 10,
			expectedUpper: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lower, upper := utils.InterquartileRange(tt.values)

			if lower != tt.expectedLower {
				t.Errorf(
					"lower = %v; want %v",
					lower,
					tt.expectedLower,
				)
			}

			if upper != tt.expectedUpper {
				t.Errorf(
					"upper = %v; want %v",
					upper,
					tt.expectedUpper,
				)
			}
		})
	}
}

func TestMean(t *testing.T) {
	tests := []struct {
		name     string
		values   []float64
		expected float64
	}{
		{
			name:     "positive integers",
			values:   []float64{1, 2, 3, 4, 5},
			expected: 3,
		},
		{
			name:     "decimal values",
			values:   []float64{1.5, 2.5, 3.5},
			expected: 2.5,
		},
		{
			name:     "negative values",
			values:   []float64{-10, -5, 0, 5, 10},
			expected: 0,
		},
		{
			name:     "single value",
			values:   []float64{42},
			expected: 42,
		},
		{
			name:     "empty slice",
			values:   []float64{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := utils.Mean(tt.values)

			if got != tt.expected {
				t.Errorf(
					"mean(%v) = %v; want %v",
					tt.values,
					got,
					tt.expected,
				)
			}
		})
	}
}
