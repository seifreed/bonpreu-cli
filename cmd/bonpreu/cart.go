package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// cmdCart dispatches the cart subcommands. The cart leg needs an imported browser
// session (import-har). Writes resolve the short retailer id to the API's product
// UUID via an anonymous product lookup, then POST apply-quantity (delta-based:
// +adds, -removes), the same call the web app's +/- buttons make.
func cmdCart(args []string) error {
	if len(args) == 0 {
		return invalidCommandf("usage: bonpreu cart <get|add|set|clear>")
	}
	switch args[0] {
	case "get":
		return cartGet(args[1:])
	case "add":
		return cartWrite(args[1:], false)
	case "set":
		return cartWrite(args[1:], true)
	case "clear":
		return cartClear(args[1:])
	default:
		return invalidCommandf("unknown cart subcommand %q (want get|add|set|clear)", args[0])
	}
}

func authedClient(cf *common) (*client.Client, error) {
	cl, _ := newClient(cf)
	loaded, err := cl.LoadAuth()
	if err != nil {
		return nil, err
	}
	if !loaded {
		return nil, fmt.Errorf("not authenticated — run `bonpreu import-har --file <har>` first")
	}
	// A stored cookie that lacks the session markers (global_sid + aws-waf-token) is
	// anonymous. Reject it here with a
	// clear message instead of letting cart/checkout hit the API and surface a raw
	// HTTP 400/403. (whoami goes through this gate too; an expired-but-well-formed
	// session still passes here and is caught by its live CheckAuth.)
	if !client.CookieLooksAuthed(cl.Cookie) {
		return nil, fmt.Errorf("the stored cookie looks anonymous (no global_sid + aws-waf-token) — re-import a logged-in session with `bonpreu import-har --file <har>`")
	}
	return cl, nil
}

func cartGet(args []string) error {
	fs, cf := newCommonFlags("cart get")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return invalidCommandf("usage: bonpreu cart get")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	cart, err := cl.GetCart()
	if err != nil {
		return err
	}
	if done, err := emitStructured(cf, cart.Raw); done {
		return err
	}
	var output strings.Builder
	if len(cart.Lines) == 0 {
		fmt.Fprintln(&output, "(cistella buida)")
	} else {
		for _, l := range cart.Lines {
			fmt.Fprintf(&output, "  %s — %s × %s€ = %s€\n", terminalText(l.ProductID), fmtQty(l.Quantity), terminalText(l.FinalPrice.Amount), terminalText(l.TotalPrices.FinalPrice.Amount))
		}
		if cart.Total.Amount != "" {
			fmt.Fprintf(&output, "  total: %s€\n", terminalText(cart.Total.Amount))
		}
	}
	return writeStdout("%s", output.String())
}

// cartWrite implements `cart add <id> <qty>` (delta +qty) and `cart set <id> <qty>`
// (delta = target − current). The current catalog price preflights --max before
// writes; accepted updates are checked against the server-priced cart line.
func cartWrite(args []string, set bool) error {
	verb := "add"
	if set {
		verb = "set"
	}
	fs, cf := newCommonFlags("cart write")
	maxEUR := fs.Float64("max", 0, "refuse if the resulting product line would exceed this many €")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	maxSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max" {
			maxSet = true
		}
	})
	if fs.NArg() != 2 {
		return invalidCommandf("usage: bonpreu cart %s <id> <qty>", verb)
	}
	retailerID := fs.Arg(0)
	qty, err := parsePositiveQty(fs.Arg(1), set)
	if err != nil {
		return err
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	// Resolve the short id to the product UUID (anonymous, verifiable) and price it.
	pd, err := cl.Product(retailerID)
	if err != nil {
		return fmt.Errorf("resolve product %s: %w", retailerID, err)
	}
	if pd.ProductID == "" {
		return fmt.Errorf("product %s has no cart product ID", retailerID)
	}

	var limit float64
	if set {
		limit, err = resolveMax(*maxEUR, maxSet)
		if err != nil {
			return err
		}
		if err := guardResultingLine(limit, pd, qty); err != nil {
			return err
		}
	}

	delta := qty
	current := float64(0)
	if set {
		current, err = currentQty(cl, pd.ProductID)
		if err != nil {
			return fmt.Errorf("read current cart quantity: %w", err)
		}
		delta = qty - current
		if delta == 0 {
			stderrInfo("cart.quantity.no_change", "cart line is already at target quantity", "operation", "cart write")
			return nil
		}
	} else if delta > 0 {
		limit, err = resolveMax(*maxEUR, maxSet)
		if err != nil {
			return err
		}
		current, err = currentQty(cl, pd.ProductID)
		if err != nil {
			return fmt.Errorf("read current cart quantity: %w", err)
		}
		if limit > 0 {
			if err := guardResultingLine(limit, pd, qty+current); err != nil {
				return err
			}
		}
	}

	cart, err := cl.ApplyQuantity([]client.CartItem{{ProductID: pd.ProductID, Quantity: delta}})
	if err != nil {
		return err
	}
	limitErr := cartUpdateLimitError(cart.Raw, pd.ProductID)
	if limitErr == nil {
		updatedCart, err := cl.GetCart()
		if err != nil {
			return fmt.Errorf("cart %s was accepted but the result could not be verified; inspect the cart before retrying: %w", verb, err)
		}
		actual, err := decimalQtySum(updatedCart.Lines, pd.ProductID)
		if err != nil {
			return fmt.Errorf("cart %s was accepted but the result could not be verified; inspect the cart before retrying: %w", verb, err)
		}
		if set && actual != qty {
			return fmt.Errorf("cart set was accepted but resulting quantity is %s, not requested %s; inspect the cart before retrying", fmtQty(actual), fmtQty(qty))
		}
		if !set && actual < current+delta {
			return fmt.Errorf("cart add was accepted but resulting quantity is %s, below the requested increase to %s; inspect the cart before retrying", fmtQty(actual), fmtQty(current+delta))
		}
		if limit > 0 {
			actualLineCents := int64(0)
			if actual > 0 {
				actualLineCents, err = cartLineTotalCents(updatedCart.Lines, pd.ProductID)
				if err != nil {
					return fmt.Errorf("cart %s was accepted but the spending limit could not be checked after the write; inspect the cart before retrying: %w", verb, err)
				}
			}
			lineEUR := float64(actualLineCents) / 100
			if lineEUR > limit {
				return fmt.Errorf("cart %s was accepted but the resulting line exceeds --max: %.2f€ > %.2f€; inspect the cart before retrying", verb, lineEUR, limit)
			}
		}
	}
	if limitErr == nil {
		stderrInfo("cart.quantity.changed", "cart line quantity updated", "operation", cf.operation, "quantity_delta", delta)
	}
	if done, err := emitStructured(cf, cart.Raw); done {
		if err != nil {
			return fmt.Errorf("cart update was accepted but confirmation output failed; inspect the cart before retrying: %w", err)
		}
		return limitErr
	}
	if limitErr != nil {
		return limitErr
	}
	if err := writeStdout("ok — %s [%s] %s (Δ%+g) — %s\n", verb, terminalText(retailerID), fmtQty(qty), delta, terminalText(pd.Name)); err != nil {
		return fmt.Errorf("cart update was accepted but confirmation output failed; inspect the cart before retrying: %w", err)
	}
	return nil
}

func cartUpdateLimitError(raw json.RawMessage, productID string) error {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: %w", err)
	}
	if shape == nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: empty result")
	}
	_, hasItems := shape["items"]
	_, hasLimited := shape["limitedItems"]
	_, hasUnavailable := shape["unavailableData"]
	if !hasItems && !hasLimited && !hasUnavailable {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: missing result fields")
	}
	var result struct {
		Items        []json.RawMessage `json:"items"`
		LimitedItems []struct {
			ProductID string `json:"productId"`
			Reason    string `json:"reason"`
		} `json:"limitedItems"`
		UnavailableData []struct {
			ProductID string `json:"productId"`
			Reason    string `json:"reason"`
		} `json:"unavailableData"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: %w", err)
	}
	if hasItems && result.Items == nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: items must be an array")
	}
	if hasLimited && result.LimitedItems == nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: limitedItems must be an array")
	}
	if hasUnavailable && result.UnavailableData == nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: unavailableData must be an array")
	}
	for _, item := range result.LimitedItems {
		if strings.TrimSpace(item.ProductID) == "" {
			return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: limited item is missing productId")
		}
		if item.ProductID == productID {
			return rejectCommand("cart_quantity_limited", "cart quantity was limited by the server (%s); inspect the cart before retrying", terminalText(firstNonEmpty(item.Reason, "quantity limit")))
		}
	}
	for _, item := range result.UnavailableData {
		if strings.TrimSpace(item.ProductID) == "" {
			return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: unavailable item is missing productId")
		}
		if item.ProductID == productID {
			return rejectCommand("cart_product_unavailable", "cart product is unavailable (%s); inspect the cart before retrying", terminalText(firstNonEmpty(item.Reason, "unavailable")))
		}
	}
	return nil
}

func cartClear(args []string) error {
	fs, cf := newCommonFlags("cart clear")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return invalidCommandf("usage: bonpreu cart clear")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	cart, err := cl.GetCart()
	if err != nil {
		return err
	}
	if len(cart.Lines) == 0 {
		stderrInfo("cart.clear.already_empty", "cart is already empty", "operation", "cart clear")
		if done, err := emitStructured(cf, cart.Raw); done {
			return err
		}
		return nil
	}
	items := make([]client.CartItem, 0, len(cart.Lines))
	for _, l := range cart.Lines {
		items = append(items, client.CartItem{ProductID: l.ProductID, Quantity: -l.Quantity})
	}
	updated, err := cl.ApplyQuantity(items)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := cartUpdateLimitError(updated.Raw, item.ProductID); err != nil {
			return err
		}
	}
	remaining, err := cl.GetCart()
	if err != nil {
		return fmt.Errorf("cart clear was accepted but the result could not be verified; inspect the cart before retrying: %w", err)
	}
	if len(remaining.Lines) > 0 {
		return fmt.Errorf("cart clear was accepted but %d cart line(s) remain; inspect the cart before retrying", len(remaining.Lines))
	}
	stderrInfo("cart.clear.completed", "cart lines cleared", "operation", cf.operation, "line_count", len(items))
	if done, err := emitStructured(cf, updated.Raw); done {
		if err != nil {
			return fmt.Errorf("cart update was accepted but confirmation output failed; inspect the cart before retrying: %w", err)
		}
		return nil
	}
	if err := writeStdout("ok — removed %d line(s)\n", len(items)); err != nil {
		return fmt.Errorf("cart update was accepted but confirmation output failed; inspect the cart before retrying: %w", err)
	}
	return nil
}

// currentQty returns the summed quantity of a product already in the active cart
// (0 if absent). A read failure is returned, not swallowed: `cart set` derives
// its delta from this, so a silent 0 would over-add the full target quantity.
func currentQty(cl *client.Client, productID string) (float64, error) {
	cart, err := cl.GetCart()
	if err != nil {
		return 0, err
	}
	return decimalQtySum(cart.Lines, productID)
}

func decimalQtySum(lines []client.CartLine, productID string) (float64, error) {
	quantity := new(big.Rat)
	for _, l := range lines {
		if l.ProductID == productID {
			line, err := ratFromDecimal(strconv.FormatFloat(l.Quantity, 'f', -1, 64))
			if err != nil {
				return 0, fmt.Errorf("invalid cart quantity for product %s: %w", productID, err)
			}
			quantity.Add(quantity, line)
		}
	}
	total, _ := quantity.Float64()
	if math.IsInf(total, 0) {
		return 0, fmt.Errorf("cart quantity for product %s exceeds the supported range", productID)
	}
	return total, nil
}

func cartLineTotalCents(lines []client.CartLine, productID string) (int64, error) {
	var total int64
	found := false
	for _, line := range lines {
		if line.ProductID != productID {
			continue
		}
		found = true
		cents, err := priceCents(line.TotalPrices.FinalPrice.Amount)
		if err != nil {
			return 0, fmt.Errorf("unparseable cart line total: %w", err)
		}
		if cents > math.MaxInt64-total {
			return 0, fmt.Errorf("cart line total exceeds the supported range")
		}
		total += cents
	}
	if !found {
		return 0, fmt.Errorf("product %s is missing from the cart response", productID)
	}
	return total, nil
}

// guardResultingLine enforces --max against the resulting product-line value.
func guardResultingLine(maxEUR float64, pd *client.ProductDetail, qty float64) error {
	if maxEUR == 0 {
		return nil
	}
	lineCents, err := resultingLineCents(pd, qty)
	if err != nil {
		return err
	}
	line := float64(lineCents) / 100
	return checkBudget(line, maxEUR, "cart line")
}

func resultingLineCents(pd *client.ProductDetail, qty float64) (int64, error) {
	cents, err := priceCents(pd.Price.Amount)
	if err != nil {
		return 0, fmt.Errorf("cannot enforce spending limit for an unpriceable cart line: %w", err)
	}
	lineCents, err := lineCents(cents, qty)
	if err != nil {
		return 0, fmt.Errorf("cannot enforce spending limit for cart line: %w", err)
	}
	return lineCents, nil
}

func parsePositiveQty(s string, allowZero bool) (float64, error) {
	bl, err := parseBasketLine("x " + s)
	if err != nil {
		return 0, err
	}
	if bl.qty == 0 && !allowZero {
		return 0, fmt.Errorf("qty must be > 0 for add (use `cart set <id> 0` to remove)")
	}
	return bl.qty, nil
}
