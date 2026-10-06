package domain

import (
	"encoding/json"
	"testing"
)

func TestMoneyExactDecimals(t *testing.T) {
	for _, tc := range []struct {
		input string
		cents Money
		valid bool
	}{
		{"0", 0, true}, {"0.01", 1, true}, {"10.5", 1050, true}, {"1000000000.00", MaxAmount, true},
		{"0.001", 0, false}, {"-1", 0, false}, {"1e3", 0, false}, {`"10"`, 0, false}, {"null", 0, false}, {"1000000001", 0, false},
	} {
		var m Money
		err := json.Unmarshal([]byte(tc.input), &m)
		if (err == nil) != tc.valid || (tc.valid && m != tc.cents) {
			t.Fatalf("%s: %d %v", tc.input, m, err)
		}
	}
}
