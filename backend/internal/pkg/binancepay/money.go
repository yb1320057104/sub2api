// Package binancepay reads authenticated incoming Binance Pay transfers.
package binancepay

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MoneyScale is the number of integer units in one USDT. Matching never uses
// floating point: the five decimal places shown to a payer are all credited.
const MoneyScale int64 = 100000

// ParseAmount accepts unsigned ordinary decimal strings. Additional decimal
// places are allowed only when they are zero; nonzero precision is never rounded.
func ParseAmount(value string) (int64, error) {
	if value == "" || len(value) > 64 {
		return 0, errors.New("binance: invalid decimal amount")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("binance: invalid decimal amount")
	}
	for _, part := range parts {
		if part == "" {
			return 0, errors.New("binance: invalid decimal amount")
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return 0, errors.New("binance: invalid decimal amount")
			}
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/MoneyScale {
		return 0, errors.New("binance: amount out of range")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 5 {
		if strings.Trim(fraction[5:], "0") != "" {
			return 0, errors.New("binance: amount exceeds five decimal places")
		}
		fraction = fraction[:5]
	}
	fraction += strings.Repeat("0", 5-len(fraction))
	tail, _ := strconv.ParseInt(fraction, 10, 64)
	units := whole * MoneyScale
	if tail > math.MaxInt64-units {
		return 0, errors.New("binance: amount out of range")
	}
	return units + tail, nil
}

// FormatAmount keeps all five decimal places, including a zero suffix.
func FormatAmount(units int64) string {
	if units < 0 {
		// Avoid negating MinInt64.
		magnitude := uint64(-(units + 1)) + 1
		return fmt.Sprintf("-%d.%05d", magnitude/uint64(MoneyScale), magnitude%uint64(MoneyScale))
	}
	return fmt.Sprintf("%d.%05d", units/MoneyScale, units%MoneyScale)
}
