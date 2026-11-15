package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// cmdCart dispatches the cart subcommands. The cart leg needs an imported browser
// session (import-har). Writes resolve the short retailer id to the API's product
// UUID via an anonymous product lookup, then POST apply-quantity (delta-based:
// +adds, -removes), the same call the web app's +/- buttons make.
func cmdCart(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bonpreu cart <get|add|set|clear>")
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
		return fmt.Errorf("unknown cart subcommand %q (want get|add|set|clear)", args[0])
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
	// anonymous — set-cookie warns about it but still saves it. Reject it here with a
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
	parseFlags(fs, args)
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
// (delta = target − current). Quantities are priced first so --max can refuse
// before any write.
func cartWrite(args []string, set bool) error {
	verb := "add"
	if set {
		verb = "set"
	}
	fs, cf := newCommonFlags("cart write")
	maxEUR := fs.Float64("max", 0, "refuse if the resulting product line would exceed this many €")
	parseFlags(fs, args)
	maxSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max" {
			maxSet = true
		}
	})
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: bonpreu cart %s <id> <qty>", verb)
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

	delta := qty
	if set {
		cur, qerr := currentQty(cl, pd.ProductID)
		if qerr != nil {
			return fmt.Errorf("read current cart quantity: %w", qerr)
		}
		delta = qty - cur
		if delta == 0 {
			stderrInfo("cart.quantity.no_change", "cart line is already at target quantity", "operation", "cart write")
			return nil
		}
	}
	if delta > 0 {
		limit, err := resolveMax(*maxEUR, maxSet)
		if err != nil {
			return err
		}
		lineQty := qty
		if !set && limit > 0 {
			current, err := currentQty(cl, pd.ProductID)
			if err != nil {
				return fmt.Errorf("read current cart quantity: %w", err)
			}
			lineQty += current
		}
		if err := guardLineAdd(limit, pd, lineQty); err != nil {
			return err
		}
	}

	cart, err := cl.ApplyQuantity([]client.CartItem{{ProductID: pd.ProductID, Quantity: delta}})
	if err != nil {
		return err
	}
	limitErr := cartUpdateLimitError(cart.Raw, pd.ProductID)
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
	var result struct {
		LimitedItems []struct {
			ProductID string `json:"productId"`
			Reason    string `json:"reason"`
		} `json:"limitedItems"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("cart update was accepted but the server result could not be inspected; check the cart before retrying: %w", err)
	}
	for _, item := range result.LimitedItems {
		if item.ProductID == productID {
			return fmt.Errorf("cart quantity was limited by the server (%s); inspect the cart before retrying", terminalText(firstNonEmpty(item.Reason, "quantity limit")))
		}
	}
	return nil
}

func cartClear(args []string) error {
	fs, cf := newCommonFlags("cart clear")
	parseFlags(fs, args)
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: bonpreu cart clear")
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
		return nil
	}
	items := make([]client.CartItem, 0, len(cart.Lines))
	for _, l := range cart.Lines {
		items = append(items, client.CartItem{ProductID: l.ProductID, Quantity: -l.Quantity})
	}
	if _, err := cl.ApplyQuantity(items); err != nil {
		return err
	}
	stderrInfo("cart.clear.completed", "cart lines cleared", "operation", cf.operation, "line_count", len(items))
	return writeStdout("ok — removed %d line(s)\n", len(items))
}

// currentQty returns the summed quantity of a product already in the active cart
// (0 if absent). A read failure is returned, not swallowed: `cart set` derives
// its delta from this, so a silent 0 would over-add the full target quantity.
func currentQty(cl *client.Client, productID string) (float64, error) {
	cart, err := cl.GetCart()
	if err != nil {
		return 0, err
	}
	var quantity float64
	for _, l := range cart.Lines {
		if l.ProductID == productID {
			quantity += l.Quantity
		}
	}
	return quantity, nil
}

// guardLineAdd enforces --max against the resulting product-line value.
func guardLineAdd(maxEUR float64, pd *client.ProductDetail, qty float64) error {
	if maxEUR == 0 {
		return nil
	}
	cents, err := priceCents(pd.Price.Amount)
	if err != nil {
		return fmt.Errorf("cannot enforce spending limit for an unpriceable cart line: %w", err)
	}
	lineCents, err := lineCents(cents, qty)
	if err != nil {
		return fmt.Errorf("cannot enforce spending limit for cart line: %w", err)
	}
	line := float64(lineCents) / 100
	return checkBudget(line, maxEUR, "cart line")
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
