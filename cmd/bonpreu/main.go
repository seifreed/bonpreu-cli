// Command bonpreu is an unofficial, agent-friendly CLI for
// compraonline.bonpreuesclat.cat (Bonpreu i Esclat online).
//
// It covers the anonymous read core — product search (single + batch),
// deterministic basket totals, product detail, and the category tree — over the
// same HTTP API the web app uses, presenting Chrome's TLS fingerprint (uTLS) so
// AWS WAF keeps it on the no-challenge path. Every command supports --json (data
// to stdout, logs to stderr) for programmatic use by agents.
package main

import (
	"fmt"
	"os"
)

// Build metadata, injected at release time via -ldflags (GoReleaser).
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() { os.Exit(run(os.Args[1:])) }

// run dispatches a command and returns the process exit code. Split from main so
// it is testable without os.Exit.
func run(args []string) int {
	if len(args) < 1 {
		usage()
		return 2
	}
	var err error
	switch args[0] {
	case "search":
		err = cmdSearch(args[1:])
	case "batch":
		err = cmdBatch(args[1:])
	case "total":
		err = cmdTotal(args[1:])
	case "product":
		err = cmdProduct(args[1:])
	case "categories":
		err = cmdCategories(args[1:])
	case "brands":
		err = cmdBrands(args[1:])
	case "login":
		err = cmdLogin(args[1:])
	case "import-har":
		err = cmdImportHar(args[1:])
	case "set-cookie":
		err = cmdSetCookie(args[1:])
	case "whoami":
		err = cmdWhoami(args[1:])
	case "cart":
		err = cmdCart(args[1:])
	case "checkout":
		err = cmdCheckout(args[1:])
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func versionString() string {
	if commit == "" {
		return version
	}
	if date != "" {
		return fmt.Sprintf("%s (%s, %s)", version, commit, date)
	}
	return fmt.Sprintf("%s (%s)", version, commit)
}

func usage() {
	fmt.Fprint(os.Stderr, `bonpreu — unofficial CLI for compraonline.bonpreuesclat.cat

USAGE:
  bonpreu <command> [flags]

READ COMMANDS (anonymous, no login):
  search <term...>        full-text product search
                          --limit N    cap results
                          --fresh      drop frozen (Congelats) + canned (Conserves)
                          --cheapest   rank by unit price (€/kg, €/L, €/u); mark the best per unit
  batch [-f file]         resolve many terms in one command — cheapest hit per
                          term, or your preferred brand if [brands] is configured
                          --fresh       drop frozen + canned first
                          --brand a,b   prefer these brands for this run
                          --no-brands   ignore configured brand preferences
                          --offers m    multi-buy deals: alert (default)|ignore|accept
  total [-f file]         deterministic basket total from '<id> [qty]' lines (summed in code)
  product <id>            product detail + price (+ ingredients/nutrition when present)
  categories [--id 0301]  category tree, or one category's products
                          --depth N     tree depth (default 1)
                          --cheapest    with --id: rank products by unit price, mark the best per unit
  brands                  list product brands (shipped snapshot; pick favourites for [brands])
                          --update      re-crawl live + print JSON (refresh the snapshot)
                          --csv         CSV instead of a table

AUTHENTICATED COMMANDS (bring your own browser session):
  login --from-browser b  lift the session from a browser's cookie store
                          (chrome|chromium|firefox|safari|edge|brave; empty = any) — easiest
  login                   open a browser, log in, capture the session
                          (needs a build with -tags browserlogin + Chrome)
  import-har --file f     import a DevTools HAR ("Save all as HAR with sensitive data")
  set-cookie '<cookie>'   seed a raw Cookie header manually (--stdin supported)
  whoami                  verify the session is accepted
  cart get                show the active cart (--json for the raw response)
  cart add <id> <qty>     add qty of a product (additive)
  cart set <id> <qty>     set a product's absolute qty (0 removes)
  cart clear              empty the cart
  checkout [status]       can the cart be checked out? (total, 35€ min, blockers)
  checkout addresses      list saved delivery destinations
  checkout slots --address <id>   list delivery slots for a destination

SPENDING GUARD (agent safety):
  --max <eur>             refuse a cart line over <eur>. Also BONPREU_MAX_EUR env
                          or [limits] max_eur in config.toml.

BRAND PREFERENCES (config.toml [brands]; build the list with the 'brands' command):
  preferred = ["Hellmann's", …]   favourites, by priority — used when present, ignored otherwise
  mode = "cheapest_among"         among matches: cheapest (default) | "strict_priority"
  on_missing = "ask"              per-item override missing → "ask" (skill prompts) | "cheapest"
  [brands.overrides]              per-term brand, e.g. "pastillas" = ["Finish"] (wins over global)

OFFERS (config.toml [offers]; or --offers on batch):
  mode = "alert"                  multi-buy deals (2a unitat, lot, 3x2): alert (ask) | ignore | accept

COMMON FLAGS (may go anywhere after the (sub)command):
  --lang ca               language: ca (default) or es
  --json                  emit raw JSON (data→stdout, logs→stderr)
  --toon                  emit TOON instead of JSON (fewer tokens; for LLM/agents)

ENV:
  BONPREU_BASE_URL        override the API host (debugging proxy, mock, staging)
  BONPREU_CONFIG_DIR      override ~/.bonpreu
  BONPREU_MAX_EUR         default spending cap (see --max)

  version | help
`)
}
