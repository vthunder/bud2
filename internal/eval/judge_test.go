package eval

import (
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"", 5, ""},
		{"hi", 2, "hi"},
	}

	for _, tc := range tests {
		result := truncate(tc.input, tc.maxLen)
		if result != tc.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.maxLen, result, tc.expected)
		}
	}
}

func TestAbs(t *testing.T) {
	if abs(-5) != 5 {
		t.Error("abs(-5) should be 5")
	}
	if abs(5) != 5 {
		t.Error("abs(5) should be 5")
	}
	if abs(0) != 0 {
		t.Error("abs(0) should be 0")
	}
}

func TestSqrt(t *testing.T) {
	// Test some known values
	tests := []struct {
		input    float64
		expected float64
	}{
		{4, 2},
		{9, 3},
		{16, 4},
		{0, 0},
	}

	for _, tc := range tests {
		result := sqrt(tc.input)
		diff := result - tc.expected
		if diff < -0.0001 || diff > 0.0001 {
			t.Errorf("sqrt(%f) = %f, want %f", tc.input, result, tc.expected)
		}
	}
}

func TestPearsonCorrelation(t *testing.T) {
	j := &Judge{}

	// Perfect correlation
	results := []JudgeResult{
		{SelfRating: 1, JudgeRating: 1},
		{SelfRating: 2, JudgeRating: 2},
		{SelfRating: 3, JudgeRating: 3},
		{SelfRating: 4, JudgeRating: 4},
		{SelfRating: 5, JudgeRating: 5},
	}
	corr := j.pearsonCorrelation(results)
	if corr < 0.99 {
		t.Errorf("Perfect correlation should be ~1.0, got %f", corr)
	}

	// No correlation (constant self)
	results2 := []JudgeResult{
		{SelfRating: 3, JudgeRating: 1},
		{SelfRating: 3, JudgeRating: 2},
		{SelfRating: 3, JudgeRating: 3},
		{SelfRating: 3, JudgeRating: 4},
		{SelfRating: 3, JudgeRating: 5},
	}
	corr2 := j.pearsonCorrelation(results2)
	if corr2 != 0 {
		t.Errorf("Zero variance should give 0 correlation, got %f", corr2)
	}
}
