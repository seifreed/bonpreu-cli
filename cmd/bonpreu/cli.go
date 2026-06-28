package main

import (
	"encoding/json"
	"flag"
	"os"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
	"github.com/seifreed/bonpreu-cli/internal/config"
)

// common flags shared by every subcommand.
type common struct {
	lang    string
	jsonOut bool
}

func addCommon(fs *flag.FlagSet) *common {
	c := &common{}
	// Empty default so newClient can tell "user passed --lang" from "unset" and
	// fall back to config [defaults] before the built-in ca.
	fs.StringVar(&c.lang, "lang", "", "language: ca (default) or es")
	fs.BoolVar(&c.jsonOut, "json", false, "emit raw JSON to stdout")
	return c
}

// newClient builds a client, resolving lang/region by precedence:
// explicit flag > config.toml [defaults] > built-in (ca, default region).
// The session cookie (for cart/checkout) is loaded from config/cache by the
// authenticated commands, not here.
func newClient(c *common) *client.Client {
	cl := client.New()
	cfg, _ := config.LoadConfig()
	if v := firstNonEmpty(c.lang, cfg.Defaults.Lang); v != "" {
		cl.Lang = v
	}
	if cfg.Defaults.RegionID != "" {
		cl.RegionID = cfg.Defaults.RegionID
	}
	return cl
}

// reorderArgs lets flags appear anywhere among positional args. The stdlib flag
// parser stops at the first positional, so `cart add 123 5 --max 50` would
// silently drop --max (dangerous for a safety flag); this hoists flags (and their
// values) ahead of a `--` terminator so a normal fs.Parse sees them all. Honours
// bool flags (no value), `--flag=value`, and an explicit `--`.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.IndexByte(name, '=') >= 0 {
				continue // --flag=value: value is in the same token
			}
			if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
				flags = append(flags, args[i+1]) // consume this flag's value
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	out := make([]string, 0, len(flags)+1+len(positional))
	out = append(out, flags...)
	out = append(out, "--") // keep positionals positional even if they start with '-'
	return append(out, positional...)
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
