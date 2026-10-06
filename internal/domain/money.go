package domain

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Money хранит сумму в сотых долях балла или копейках.
type Money int64

const MaxAmount Money = 1_000_000_000_00

func (m Money) String() string               { return fmt.Sprintf("%d.%02d", m/100, m%100) }
func (m Money) MarshalJSON() ([]byte, error) { return []byte(m.String()), nil }
func (m *Money) UnmarshalJSON(data []byte) error {
	s := string(bytes.TrimSpace(data))
	parts := strings.Split(s, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 10 {
		return ErrInvalidAmount
	}
	for _, p := range parts {
		if len(p) == 0 {
			return ErrInvalidAmount
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return ErrInvalidAmount
			}
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return ErrInvalidAmount
	}
	var fraction int64
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return ErrInvalidAmount
		}
		fraction, _ = strconv.ParseInt(parts[1], 10, 64)
		if len(parts[1]) == 1 {
			fraction *= 10
		}
	}
	value := Money(whole*100 + fraction)
	if value > MaxAmount {
		return ErrInvalidAmount
	}
	*m = value
	return nil
}
