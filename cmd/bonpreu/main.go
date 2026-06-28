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

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "search":
		err = cmdSearch(os.Args[2:])
	case "batch":
		err = cmdBatch(os.Args[2:])
	case "total":
		err = cmdTotal(os.Args[2:])
	case "product":
		err = cmdProduct(os.Args[2:])
	case "categories":
		err = cmdCategories(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
                          --limit N   cap results
                          --fresh     drop frozen (Congelats) + canned (Conserves)
  batch [-f file]         search many terms in one command (top hit per term)
                          --fresh     apply to every term's top hit
  total [-f file]         deterministic basket total from '<id> [qty]' lines (summed in code)
  product <id>            product detail + price (+ ingredients/nutrition when present)
  categories [--id 0301]  category tree, or one category's products
                          --depth N   tree depth (default 1)

COMMON FLAGS (may go anywhere after the (sub)command):
  --lang ca               language: ca (default) or es
  --json                  emit raw JSON (data→stdout, logs→stderr)

  version | help
`)
}
