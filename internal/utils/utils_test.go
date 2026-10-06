package utils

import "testing"

func TestValidateLuhn(t *testing.T) {
	for _, tc := range []struct {
		number string
		want   bool
	}{
		{"79927398713", true}, {"12345678903", true}, {"4532015112830366", true}, {"4012888888881881", true}, {"5555555555554444", true},
		{"", false}, {"0", false}, {"0000", false}, {"79927398714", false}, {"-18", false}, {"12x", false}, {" 79927398713", false},
	} {
		if got := ValidateLuhn(tc.number); got != tc.want {
			t.Errorf("%s: %v", tc.number, got)
		}
	}
}
