package core

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Paise represents an INR amount in paise (1/100 of a rupee).
//
// Canonical money type across Pikshipp. All wallet operations, rate cards,
// charges, and ledger entries use Paise. Floats are never used for money.
//
// Paise is signed (int64) so debit/credit can be expressed natively where
// useful — wallet balance can be negative within the configured grace cap.
type Paise int64

const (
	// MaxPaise is the maximum representable amount (~₹9.2×10^16).
	MaxPaise Paise = math.MaxInt64
	// MinPaise is the minimum (most-negative) representable amount.
	MinPaise Paise = math.MinInt64
)

// FromRupees converts a whole-rupee amount to Paise. Panics on overflow.
//
// For fractional amounts (e.g., GST), prefer FromRupeesPaise.
func FromRupees(rupees int64) Paise {
	if rupees > math.MaxInt64/100 || rupees < math.MinInt64/100 {
		panic("core.FromRupees: rupees overflow")
	}
	return Paise(rupees * 100)
}

// FromRupeesPaise constructs a Paise value from rupees and paise components.
// paise must be in [0, 99]; for negative values pass a negative rupees component.
//
//	FromRupeesPaise(50, 25)  ==  5025  // ₹50.25
//	FromRupeesPaise(-50, 0)  == -5000  // -₹50.00
func FromRupeesPaise(rupees int64, paise int64) Paise {
	if paise < 0 || paise > 99 {
		panic("core.FromRupeesPaise: paise out of [0,99]")
	}
	switch {
	case rupees > 0:
		return Paise(rupees*100 + paise)
	case rupees < 0:
		return Paise(rupees*100 - paise)
	default:
		return Paise(paise)
	}
}

// FromMinor returns a Paise from its int64 minor-unit value. Identity, but
// makes intent explicit at call sites where we receive integer amounts from
// vendors or DB.
func FromMinor(minor int64) Paise { return Paise(minor) }

// Minor returns the underlying int64 paise count.
func (p Paise) Minor() int64 { return int64(p) }

// Rupees returns the rupee component (truncated toward zero).
func (p Paise) Rupees() int64 { return int64(p) / 100 }

// PaiseComponent returns the paise component (sign-following).
func (p Paise) PaiseComponent() int64 { return int64(p) % 100 }

// IsZero reports whether the amount is zero paise.
func (p Paise) IsZero() bool { return p == 0 }

// IsNegative reports whether the amount is strictly negative.
func (p Paise) IsNegative() bool { return p < 0 }

// IsPositive reports whether the amount is strictly positive.
func (p Paise) IsPositive() bool { return p > 0 }

// Add returns p+other and a bool indicating no-overflow. Always check the bool.
func (p Paise) Add(other Paise) (Paise, bool) {
	sum := p + other
	if (other > 0 && sum < p) || (other < 0 && sum > p) {
		return sum, false
	}
	return sum, true
}

// AddOrPanic adds and panics on overflow. Use only when the call site
// guarantees no overflow (e.g., summing two amounts ≤ ₹100M).
func (p Paise) AddOrPanic(other Paise) Paise {
	sum, ok := p.Add(other)
	if !ok {
		panic(fmt.Sprintf("core.Paise.AddOrPanic: overflow %d + %d", p, other))
	}
	return sum
}

// Sub returns p-other with overflow check.
func (p Paise) Sub(other Paise) (Paise, bool) {
	diff := p - other
	if (other > 0 && diff > p) || (other < 0 && diff < p) {
		return diff, false
	}
	return diff, true
}

// SubOrPanic subtracts and panics on overflow.
func (p Paise) SubOrPanic(other Paise) Paise {
	diff, ok := p.Sub(other)
	if !ok {
		panic(fmt.Sprintf("core.Paise.SubOrPanic: overflow %d - %d", p, other))
	}
	return diff
}

// MulInt scales by an integer factor with overflow check.
func (p Paise) MulInt(factor int64) (Paise, bool) {
	if factor == 0 || p == 0 {
		return 0, true
	}
	result := Paise(int64(p) * factor)
	if result/Paise(factor) != p {
		return result, false
	}
	return result, true
}

// MulPercent computes p × (percentBp / 10000), where percentBp is basis points.
// Truncates toward zero. Returns overflow status.
//
//	FromRupees(100).MulPercent(1800) == ₹18.00 (18% as 1800 bp)
func (p Paise) MulPercent(percentBp int64) (Paise, bool) {
	if percentBp < 0 {
		return 0, false
	}
	if percentBp != 0 && int64(p) > math.MaxInt64/percentBp {
		return 0, false
	}
	return Paise(int64(p) * percentBp / 10000), true
}

// String returns "₹X.XX" or "-₹X.XX" — no thousands separators (UI handles those).
func (p Paise) String() string {
	abs := p
	sign := ""
	if p < 0 {
		sign = "-"
		abs = -p
	}
	return fmt.Sprintf("%s₹%d.%02d", sign, int64(abs)/100, int64(abs)%100)
}

// Format implements fmt.Formatter for %v, %s, %d.
//
//	%d — minor (paise) integer.
//	%s, %v — formatted "₹X.XX".
func (p Paise) Format(s fmt.State, verb rune) {
	switch verb {
	case 'd':
		fmt.Fprint(s, int64(p))
	case 'v', 's':
		fmt.Fprint(s, p.String())
	default:
		fmt.Fprintf(s, "%%!%c(core.Paise=%d)", verb, int64(p))
	}
}

// MarshalJSON serializes Paise as integer paise (never as a decimal rupee).
func (p Paise) MarshalJSON() ([]byte, error) {
	return strconv.AppendInt(nil, int64(p), 10), nil
}

// UnmarshalJSON parses an integer paise value.
func (p *Paise) UnmarshalJSON(data []byte) error {
	n, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("core.Paise.UnmarshalJSON: %w", err)
	}
	*p = Paise(n)
	return nil
}

// Money-related sentinel errors.
var (
	ErrNegativeMoney = errors.New("core: amount cannot be negative")
	ErrZeroMoney     = errors.New("core: amount must be positive")
	ErrMoneyOverflow = errors.New("core: arithmetic overflow")
)
