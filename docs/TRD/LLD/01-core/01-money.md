# Core: Money (`internal/core/money.go`)

> The most critical type in the system. Wrong here = wrong everywhere.

## Purpose

A type-safe, integer-arithmetic representation of Indian Rupees in **paise**. All money operations across the platform use this type. No floats. Anywhere.

## Dependencies

None — this is core.

## Public API

```go
// Package core provides fundamental types used across all Pikshipp modules.
package core

// Paise represents a monetary amount in Indian paise (1/100 of a rupee).
//
// Paise is the canonical money type across Pikshipp. All wallet operations,
// rate cards, charges, and ledger entries use Paise. Floats are never used
// for money.
//
// Paise is signed (int64) to support debit/credit semantics natively where
// useful (e.g., wallet balance can be negative within grace cap).
type Paise int64

// MaxPaise is the maximum representable paise. About ₹92,233,720,368,547,758.
// Far above any conceivable transaction; a hard ceiling to detect overflow.
const MaxPaise Paise = math.MaxInt64

// MinPaise is the minimum (most-negative) value.
const MinPaise Paise = math.MinInt64

// FromRupees converts a whole-rupee amount to Paise. Panics on overflow.
//
// Use only for human-input amounts where the integer rupee is the natural unit.
// For fractional amounts (e.g., GST), prefer FromRupeesPaise(rupees, paise).
func FromRupees(rupees int64) Paise {
    if rupees > math.MaxInt64/100 || rupees < math.MinInt64/100 {
        panic("core.FromRupees: rupees overflow")
    }
    return Paise(rupees * 100)
}

// FromRupeesPaise constructs a Paise value from rupees and paise components.
// The paise component must be in [0, 99] (positive only); for negative values
// pass a negative rupees component.
//
// Examples:
//   FromRupeesPaise(50, 25)   == 5025 paise = ₹50.25
//   FromRupeesPaise(-50, 25)  == -4975 paise = -₹49.75 (NOT what you want!)
//   FromRupeesPaise(-50, 0)   == -5000 paise = -₹50.00 (correct)
func FromRupeesPaise(rupees int64, paise int64) Paise {
    if paise < 0 || paise > 99 {
        panic("core.FromRupeesPaise: paise out of [0,99]")
    }
    if rupees > 0 {
        return Paise(rupees*100 + paise)
    }
    if rupees < 0 {
        return Paise(rupees*100 - paise)
    }
    return Paise(paise)
}

// FromMinor returns a Paise from its int64 minor-unit value. Identity, but
// makes intent explicit at call sites where we receive integer amounts from
// vendors or DB.
func FromMinor(minor int64) Paise {
    return Paise(minor)
}

// Minor returns the underlying int64 paise count.
func (p Paise) Minor() int64 {
    return int64(p)
}

// Rupees returns the rupee component (truncated toward zero).
//
// Note: Rupees() + Paise() does NOT round-trip a negative amount in the way
// you might expect. Use only for display.
func (p Paise) Rupees() int64 {
    return int64(p) / 100
}

// PaiseComponent returns the paise component (always in [0, 99] for positive
// values; negative for negative values).
func (p Paise) PaiseComponent() int64 {
    return int64(p) % 100
}

// IsZero returns true if the amount is zero paise.
func (p Paise) IsZero() bool {
    return p == 0
}

// IsNegative returns true if the amount is negative.
func (p Paise) IsNegative() bool {
    return p < 0
}

// IsPositive returns true if the amount is strictly positive.
func (p Paise) IsPositive() bool {
    return p > 0
}

// Add returns p + other. Returns the result and a bool indicating whether
// overflow occurred (in which case the result is the wraparound value).
//
// Always check the bool. Domain code should use AddOrPanic if it has a
// pre-validated invariant; otherwise check and return an error.
func (p Paise) Add(other Paise) (Paise, bool) {
    sum := p + other
    if (other > 0 && sum < p) || (other < 0 && sum > p) {
        return sum, false  // overflow
    }
    return sum, true
}

// AddOrPanic adds and panics on overflow.
// Use only when overflow is provably impossible (e.g., adding two amounts
// each ≤ ₹100M).
func (p Paise) AddOrPanic(other Paise) Paise {
    sum, ok := p.Add(other)
    if !ok {
        panic(fmt.Sprintf("core.Paise.AddOrPanic: overflow %d + %d", p, other))
    }
    return sum
}

// Sub returns p - other with overflow check.
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

// MulInt scales by an integer factor (e.g., quantity).
// Returns overflow status.
func (p Paise) MulInt(factor int64) (Paise, bool) {
    if factor == 0 || p == 0 {
        return 0, true
    }
    result := Paise(int64(p) * factor)
    if result/Paise(factor) != p {
        return result, false  // overflow
    }
    return result, true
}

// MulPercent computes p * (percentBp / 10000) where percentBp is basis points.
// Examples:
//   FromRupees(100).MulPercent(1800) == ₹18.00 (18% as 1800 bp)
//
// Uses int64 intermediate; rounded toward zero. Returns overflow status.
func (p Paise) MulPercent(percentBp int64) (Paise, bool) {
    if percentBp < 0 {
        return 0, false  // we don't support negative percentages
    }
    // p * percentBp / 10000
    if percentBp != 0 && int64(p) > math.MaxInt64/percentBp {
        return 0, false
    }
    return Paise(int64(p) * percentBp / 10000), true
}

// String returns "₹X.XX" or "-₹X.XX" formatted Indian style (no thousands separators
// at this layer; UI formats with separators).
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
// %d gives the minor (paise) integer; %s and %v give the formatted string.
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
```

## Internal types

None — Paise is the only export.

## Database mapping

`Paise` is stored as PostgreSQL `BIGINT`.

### sqlc configuration

In `sqlc.yaml`:

```yaml
overrides:
  - go_type:
      import: "github.com/pikshipp/pikshipp/internal/core"
      package: "core"
      type: "Paise"
    db_type: "bigint"
    nullable: false
  - go_type:
      import: "github.com/pikshipp/pikshipp/internal/core"
      package: "core"
      type: "*Paise"
    db_type: "bigint"
    nullable: true
```

This makes sqlc-generated code use `core.Paise` for any `BIGINT NOT NULL` column whose name suggests money (you may need additional rules to scope by column name patterns).

For columns that are integer counts but not money (e.g., `attempt_no`), use plain `int64` — the override only applies to `Paise` aliases in queries.

## JSON serialization

Paise should serialize as **integer paise** in JSON, not as rupees-with-decimal. This is unambiguous and locale-free.

```go
// MarshalJSON implements json.Marshaler.
func (p Paise) MarshalJSON() ([]byte, error) {
    return strconv.AppendInt(nil, int64(p), 10), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *Paise) UnmarshalJSON(data []byte) error {
    n, err := strconv.ParseInt(string(data), 10, 64)
    if err != nil {
        return fmt.Errorf("core.Paise.UnmarshalJSON: %w", err)
    }
    *p = Paise(n)
    return nil
}
```

In API responses, the dashboard formats the integer as ₹X.XX for display.

## Validation

`Paise` has no validation at construction (any int64 is valid). Domain-level validation (non-negative for amounts, within range for rate cards) is at the call site.

## Implementation notes

### Why int64

- Indian e-commerce shipments rarely exceed ₹100k; a ₹100M shipment is far below int64's range (₹92,000,000,000,000,000).
- int64 arithmetic is fast and exactly reproducible.
- Decimal/big-int libraries are overkill and slower for our use case.

### Why paise (not millipaise / micro-rupees)

- Paise is the smallest legal denomination of INR.
- Carrier rate cards, GST tables, and Indian banking systems all express amounts in paise (or with explicit ₹.XX precision).
- No fractional paise exist in the wild.

### Overflow strategy

- Domain code should treat `Add`/`Sub` overflow as a **bug**, not a runtime condition.
- Wrap with `AddOrPanic` only when call-site arithmetic guarantees safety.
- For untrusted inputs (e.g., parsing rate cards from CSV), check bounds explicitly.

### Thread safety

`Paise` is a value type. No locking needed. Pass by value freely.

## Errors

```go
// Sentinel errors related to Paise validation.
var (
    ErrNegativeMoney      = errors.New("core: amount cannot be negative")
    ErrZeroMoney          = errors.New("core: amount must be positive")
    ErrMoneyOverflow      = errors.New("core: arithmetic overflow")
)
```

## Testing

### Unit tests (`money_test.go`)

```go
func TestPaise_FromRupees(t *testing.T) {
    cases := []struct {
        in   int64
        want Paise
    }{
        {0, 0},
        {1, 100},
        {100, 10000},
        {-50, -5000},
    }
    for _, c := range cases {
        got := FromRupees(c.in)
        if got != c.want {
            t.Errorf("FromRupees(%d) = %d; want %d", c.in, got, c.want)
        }
    }
}

func TestPaise_FromRupeesPaise(t *testing.T) {
    cases := []struct {
        rupees int64
        paise  int64
        want   Paise
    }{
        {0, 0, 0},
        {1, 0, 100},
        {1, 50, 150},
        {-1, 0, -100},
        {-1, 50, -150},
        {0, 99, 99},
    }
    for _, c := range cases {
        got := FromRupeesPaise(c.rupees, c.paise)
        if got != c.want {
            t.Errorf("FromRupeesPaise(%d, %d) = %d; want %d", c.rupees, c.paise, got, c.want)
        }
    }
}

func TestPaise_FromRupeesPaise_PaiseOutOfRange(t *testing.T) {
    cases := []struct{ rupees, paise int64 }{
        {0, 100}, {0, -1}, {1, 200},
    }
    for _, c := range cases {
        defer func() { recover() }()
        FromRupeesPaise(c.rupees, c.paise)
        t.Errorf("expected panic for FromRupeesPaise(%d, %d)", c.rupees, c.paise)
    }
}

func TestPaise_AddOverflow(t *testing.T) {
    _, ok := MaxPaise.Add(Paise(1))
    if ok {
        t.Error("expected overflow on MaxPaise + 1")
    }
}

func TestPaise_String(t *testing.T) {
    cases := []struct {
        in   Paise
        want string
    }{
        {0, "₹0.00"},
        {100, "₹1.00"},
        {150, "₹1.50"},
        {-150, "-₹1.50"},
        {123456789, "₹1234567.89"},
    }
    for _, c := range cases {
        if got := c.in.String(); got != c.want {
            t.Errorf("Paise(%d).String() = %q; want %q", int64(c.in), got, c.want)
        }
    }
}

func TestPaise_JSON(t *testing.T) {
    p := Paise(15050)
    data, err := json.Marshal(p)
    if err != nil { t.Fatal(err) }
    if string(data) != "15050" {
        t.Errorf("json: got %q, want %q", data, "15050")
    }

    var unmarshaled Paise
    err = json.Unmarshal([]byte("15050"), &unmarshaled)
    if err != nil { t.Fatal(err) }
    if unmarshaled != p {
        t.Errorf("unmarshal: got %d, want %d", unmarshaled, p)
    }
}
```

### Benchmarks (`money_bench_test.go`)

```go
func BenchmarkPaise_Add(b *testing.B) {
    p := Paise(1000)
    other := Paise(50)
    for i := 0; i < b.N; i++ {
        _, _ = p.Add(other)
    }
}

func BenchmarkPaise_String(b *testing.B) {
    p := Paise(123456)
    for i := 0; i < b.N; i++ {
        _ = p.String()
    }
}

func BenchmarkPaise_JSONMarshal(b *testing.B) {
    p := Paise(123456)
    for i := 0; i < b.N; i++ {
        _, _ = json.Marshal(p)
    }
}
```

Targets: `Add` < 1ns; `String` < 100ns; `JSONMarshal` < 50ns.

## Performance budget

- Construction (`FromRupees`, `FromMinor`): no allocation.
- `Add`, `Sub`, `MulInt`, `MulPercent`: no allocation, < 1ns.
- `String`: ~50–100ns; one allocation.
- `MarshalJSON`: ~30–50ns; one allocation.

## Open questions

- **GST computation**: should `Paise.MulPercent(1800)` round-half-up or truncate? Currently truncates (round toward zero). For GST, banks typically round-half-up. Lock at LLD review.
- **Negative paise handling in `String`**: parentheses (₹(1.50)) vs. minus sign (-₹1.50)? Currently minus sign. Confirm with design.
- **Currency union type**: when (if) we add USD/AED, do we evolve to a `Currency` enum + `Money{currency, minor}`? Sketched as deferred; revisit when needed.

## References

- HLD `00-tenets.md` § 3.5 (money in paise).
- HLD `03-services/04-wallet-and-ledger.md` (wallet uses Paise).
