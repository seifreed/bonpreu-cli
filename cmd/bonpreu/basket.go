package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
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
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	var lines []basketLine
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	ln := 0
	for sc.Scan() {
		ln++
		t := stripComment(sc.Text())
		if t == "" {
			continue
		}
		bl, err := parseBasketLine(t)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", ln, err)
		}
		lines = append(lines, bl)
	}
	return lines, sc.Err()
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
		if err != nil || q < 0 {
			return basketLine{}, fmt.Errorf("invalid qty %q (want a non-negative number)", f[1])
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
	if err != nil {
		return 0, fmt.Errorf("unparseable price %q", s)
	}
	return int64(math.Round(v * 100)), nil
}

// centsStr renders integer cents as a "35.00"-style euro string.
func centsStr(c int64) string {
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}

func fmtQty(q float64) string {
	if q == math.Trunc(q) {
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
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if t := stripComment(sc.Text()); t != "" {
			out = append(out, t)
		}
	}
	return out, sc.Err()
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
