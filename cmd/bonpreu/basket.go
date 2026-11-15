package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
)

type basketLine struct {
	id  string
	qty float64
}

// collectBasket reads basket lines from a file/stdin (-f, one '<id> [qty]' per
// line, '#' comments skipped) or, with no file, from positional args (each a bare
// id with qty 1). A per-line missing qty defaults to 1.
func collectBasket(file string, posArgs []string) ([]basketLine, error) {
	if file == "" {
		lines := make([]basketLine, 0, len(posArgs))
		for _, a := range posArgs {
			lines = append(lines, basketLine{id: a, qty: 1})
		}
		return lines, nil
	}
	var lines []basketLine
	err := scanSource(file, func(ln int, t string) error {
		bl, perr := parseBasketLine(t)
		if perr != nil {
			return fmt.Errorf("line %d: %w", ln, perr)
		}
		lines = append(lines, bl)
		return nil
	})
	return lines, err
}

// scanSource invokes fn for each non-empty, comment-stripped line read from a
// file (or stdin when path is "-"), with the line's 1-based number for error
// messages. It owns opening/closing the source and the large-line buffer.
func scanSource(path string, fn func(lineNo int, text string) error) error {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	ln := 0
	for sc.Scan() {
		ln++
		if t := stripComment(sc.Text()); t != "" {
			if err := fn(ln, t); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// stripComment drops a '#' comment — whole-line or trailing — and trims, so basket
// and set-many files can annotate ids with the product name ("00944 1 # Tomàquet").
func stripComment(s string) string {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func parseBasketLine(s string) (basketLine, error) {
	f := strings.Fields(s)
	switch len(f) {
	case 1:
		return basketLine{id: f[0], qty: 1}, nil
	case 2:
		q, err := strconv.ParseFloat(f[1], 64)
		// Reject NaN/±Inf explicitly: ParseFloat accepts "NaN"/"Inf", and the q<0
		// guard lets them through (NaN<0 and +Inf<0 are both false), which would
		// silently corrupt a basket total (Inf→garbage cents, NaN→a dropped line)
		// and bypass the --max guard (NaN makes delta>0 false). Quantities must be
		// finite and non-negative.
		if err != nil || q < 0 || math.IsInf(q, 0) || math.IsNaN(q) {
			return basketLine{}, fmt.Errorf("invalid qty %q (want a finite, non-negative number)", f[1])
		}
		return basketLine{id: f[0], qty: q}, nil
	default:
		return basketLine{}, fmt.Errorf("expected '<id> [qty]', got %q", s)
	}
}

// priceCents parses a "1.69"-style euro price into integer cents, so totals sum
// exactly with no floating-point drift across many lines.
func priceCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty price")
	}
	v, err := strconv.ParseFloat(s, 64)
	invalid := err == nil && (math.IsNaN(v) || math.IsInf(v, 0) || v < 0)
	var amount *big.Rat
	if err == nil && !invalid {
		amount, err = ratFromDecimal(s)
	}
	if err != nil {
		return 0, fmt.Errorf("unparseable price %q", s)
	}
	if invalid {
		return 0, fmt.Errorf("invalid price %q (want a finite, non-negative amount)", s)
	}
	amount.Mul(amount, big.NewRat(100, 1))
	cents := roundRat(amount)
	if !cents.IsInt64() {
		return 0, fmt.Errorf("price %q exceeds the supported range", s)
	}
	return cents.Int64(), nil
}

func ratFromDecimal(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid decimal %q", s)
	}
	return r, nil
}

func roundRat(r *big.Rat) *big.Int {
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(r.Num(), r.Denom(), remainder)
	if remainder.Lsh(remainder, 1).Cmp(r.Denom()) >= 0 {
		integer.Add(integer, big.NewInt(1))
	}
	return integer
}

// lineCents returns a line subtotal in whole cents: unit-price cents × qty,
// rounded. Shared by the basket total and the --max budget guard so both price
// a line identically.
func lineCents(unitCents int64, qty float64) (int64, error) {
	if unitCents < 0 || qty < 0 {
		return 0, fmt.Errorf("invalid price or quantity")
	}
	quantity, err := ratFromDecimal(strconv.FormatFloat(qty, 'g', -1, 64))
	if err != nil {
		return 0, fmt.Errorf("invalid price or quantity: %w", err)
	}
	subtotal := new(big.Rat).Mul(big.NewRat(unitCents, 1), quantity)
	cents := roundRat(subtotal)
	if !cents.IsInt64() {
		return 0, fmt.Errorf("line subtotal exceeds the supported range")
	}
	return cents.Int64(), nil
}

// centsStr renders integer cents as a "35.00"-style euro string. Negative values
// are formatted on their magnitude with the sign prepended, so -105 → "-1.05"
// rather than "-1.-5" from a naive %d.%02d on the raw quotient/remainder.
func centsStr(c int64) string {
	if c < 0 {
		return fmt.Sprintf("-%d.%02d", -(c / 100), -(c % 100))
	}
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}

func fmtQty(q float64) string {
	if q == math.Trunc(q) && q >= math.MinInt64 && q < float64(math.MaxInt64) {
		return strconv.FormatInt(int64(q), 10)
	}
	return strconv.FormatFloat(q, 'g', -1, 64)
}

// collectLines reads non-empty, comment-stripped lines from a file/stdin, or
// returns the positional args verbatim. Used by batch for free-text search terms.
func collectLines(file string, posArgs []string) ([]string, error) {
	if file == "" {
		return posArgs, nil
	}
	var out []string
	err := scanSource(file, func(_ int, t string) error {
		out = append(out, t)
		return nil
	})
	return out, err
}

// plural renders "1 línia" / "3 línies" — count + the right Catalan word.
func plural(n int, one, many string) string {
	w := many
	if n == 1 {
		w = one
	}
	return fmt.Sprintf("%d %s", n, w)
}
