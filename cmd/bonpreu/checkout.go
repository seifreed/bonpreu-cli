package main

import (
	"fmt"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

var openCheckoutBrowser = client.CheckoutInBrowser

// cmdCheckout dispatches checkout inspection and slot-reservation commands.
// Order confirmation and payment remain in Bonpreu's official browser UI.
func cmdCheckout(args []string) error {
	if len(args) == 0 {
		return checkoutStatus(nil)
	}
	switch args[0] {
	case "status":
		return checkoutStatus(args[1:])
	case "addresses":
		return checkoutAddresses(args[1:])
	case "slots":
		return checkoutSlots(args[1:])
	case "reserve":
		return checkoutReserve(args[1:])
	case "browser":
		return checkoutBrowser(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			// No subcommand → treat as `status` with flags.
			return checkoutStatus(args)
		}
		return fmt.Errorf("unknown checkout subcommand %q (want status|addresses|slots|reserve|browser)", args[0])
	}
}

func checkoutBrowser(args []string) error {
	fs, cf := newCommonFlags("checkout browser")
	parseFlags(fs, args)
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: bonpreu checkout browser")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	stderrInfo("checkout.browser.started", "opening official checkout in browser", "operation", cf.operation)
	if err := openCheckoutBrowser(cl.BaseURL, cl.Cookie); err != nil {
		return err
	}
	stderrInfo("checkout.browser.closed", "official checkout browser closed; purchase status was not verified", "operation", cf.operation)
	if done, err := emitStructured(cf, map[string]any{"browser_closed": true, "order_placed": nil}); done {
		return err
	}
	return writeStdoutln("navegador cerrado; el CLI no ha confirmado si se realizó la compra")
}

func checkoutReserve(args []string) error {
	fs, cf := newCommonFlags("checkout reserve")
	address := fs.String("address", "", "delivery destination id (from `checkout addresses`)")
	slot := fs.String("slot", "", "delivery slot id (from `checkout slots`)")
	region := fs.String("region", "", "delivery region id (defaults to the authenticated session)")
	externalAddress := fs.String("external-address", "", "optional custom delivery address")
	parseFlags(fs, args)
	if fs.NArg() != 0 || strings.TrimSpace(*address) == "" || strings.TrimSpace(*slot) == "" {
		return fmt.Errorf("usage: bonpreu checkout reserve --address <id> --slot <id> [--region <id>] [--external-address <text>]")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	regionID := firstNonEmpty(*region, cl.RegionID)
	if strings.TrimSpace(regionID) == "" {
		return fmt.Errorf("missing delivery region — pass --region or save region_id in the authenticated session/config")
	}
	if _, err := cl.ReserveSlot(regionID, *address, *slot, *externalAddress); err != nil {
		return err
	}
	stderrInfo("checkout.slot.reserved", "delivery slot reserved", "operation", cf.operation)
	result := map[string]any{"reserved": true, "delivery_destination_id": *address, "slot_id": *slot}
	if done, err := emitStructured(cf, result); done {
		return err
	}
	return writeStdout("franja %s reservada para la dirección %s\n", terminalText(*slot), terminalText(*address))
}

// checkoutStatus reports whether the active cart can be checked out, and if not,
// what's blocking it (below the 35€ minimum, no slot booked, empty basket).
func checkoutStatus(args []string) error {
	fs, cf := newCommonFlags("checkout")
	parseFlags(fs, args)
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: bonpreu checkout [status]")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	acg, total, raw, err := cl.CheckoutStatus()
	if err != nil {
		return err
	}
	stderrInfo("checkout.status.checked", "checkout readiness checked",
		"operation", cf.operation, "ready", acg.CanCheckout, "restriction_count", len(acg.CheckoutRestrictions))
	if done, err := emitStructured(cf, raw); done {
		return err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "total: %s€  (mínim %s€)\n", terminalText(firstNonEmpty(total.Amount, "0.00")), terminalText(firstNonEmpty(acg.MinimumCheckoutThreshold.Amount, "?")))
	if acg.CanCheckout {
		fmt.Fprintln(&output, "es pot validar la compra ✓")
	} else {
		fmt.Fprintf(&output, "NO es pot validar encara — manca: %s\n", terminalText(restrictionsCa(acg.CheckoutRestrictions)))
	}
	return writeStdout("%s", output.String())
}

// restrictionsCa renders the API restriction codes as readable Catalan reasons.
func restrictionsCa(codes []string) string {
	m := map[string]string{
		"NOT_REACHED_THRESHOLD": "no s'arriba al mínim",
		"MISSING_SLOT":          "falta reservar franja d'entrega",
		"EMPTY_BASKET":          "cistella buida",
	}
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, firstNonEmpty(m[c], c))
	}
	if len(out) == 0 {
		return "(cap)"
	}
	return strings.Join(out, ", ")
}

func checkoutAddresses(args []string) error {
	fs, cf := newCommonFlags("checkout addresses")
	parseFlags(fs, args)
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: bonpreu checkout addresses")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	raw, err := cl.Addresses()
	if err != nil {
		return err
	}
	return emitRawStructured(cf, raw)
}

func checkoutSlots(args []string) error {
	fs, cf := newCommonFlags("checkout slots")
	addr := fs.String("address", "", "delivery destination id (from `checkout addresses`)")
	parseFlags(fs, args)
	if fs.NArg() != 0 || *addr == "" {
		return fmt.Errorf("usage: bonpreu checkout slots --address <deliveryDestinationId>")
	}
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	raw, err := cl.Slots(*addr)
	if err != nil {
		// The CLI hits the same /api/ecomslots/v1/web/slots-offering the website
		// does; a 404 here is the slot service itself returning nothing (the site
		// shows a "slot service down" card for the same case) — not a bad id. Say
		// so honestly, and still point at `addresses` in case the id is wrong.
		if status, _ := client.HTTPStatus(err); status == 404 {
			return fmt.Errorf("no slots offered for address %s right now — Bonpreu's slot service "+
				"returned nothing (often temporarily down, or no open slots for the area); "+
				"verify the id with `bonpreu checkout addresses`", *addr)
		}
		return err
	}
	return emitRawStructured(cf, raw)
}
