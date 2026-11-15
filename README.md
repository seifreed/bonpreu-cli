<p align="center">
  <img src="https://img.shields.io/badge/bonpreu--cli-Grocery%20CLI-blue?style=for-the-badge" alt="bonpreu-cli">
</p>

<h1 align="center">bonpreu-cli</h1>

<p align="center">
  <strong>Unofficial, agent-friendly CLI for Bonpreu i Esclat online — search the catalog, read prices, price a basket, and build a cart</strong>
</p>

<p align="center">
  <a href="https://github.com/seifreed/bonpreu-cli/blob/main/go.mod"><img src="https://img.shields.io/badge/go-1.26%2B-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go Version"></a>
  <a href="https://github.com/seifreed/bonpreu-cli/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-green?style=flat-square" alt="License"></a>
  <a href="https://github.com/seifreed/bonpreu-cli"><img src="https://img.shields.io/badge/single%20binary-no%20runtime%20deps-brightgreen?style=flat-square" alt="Single binary"></a>
  <a href="https://github.com/seifreed/bonpreu-cli"><img src="https://img.shields.io/badge/output-JSON%20%7C%20human-blue?style=flat-square" alt="Output"></a>
</p>

<p align="center">
  <a href="https://github.com/seifreed/bonpreu-cli/stargazers"><img src="https://img.shields.io/github/stars/seifreed/bonpreu-cli?style=flat-square" alt="GitHub Stars"></a>
  <a href="https://github.com/seifreed/bonpreu-cli/issues"><img src="https://img.shields.io/github/issues/seifreed/bonpreu-cli?style=flat-square" alt="GitHub Issues"></a>
  <a href="https://buymeacoffee.com/seifreed"><img src="https://img.shields.io/badge/Buy%20Me%20a%20Coffee-support-yellow?style=flat-square&logo=buy-me-a-coffee&logoColor=white" alt="Buy Me a Coffee"></a>
</p>

---

## Overview

**bonpreu-cli** is a single static Go binary that drives the (unofficial) `compraonline.bonpreuesclat.cat` HTTP API: full-text product search, unit-price ("cheapest per kg/L") ranking, deterministic basket totals, product detail with nutrition/offers, the category tree, a brand catalogue with configurable brand preferences, multi-buy offer flagging, and an authenticated cart leg. Every command speaks structured `--json` (data to stdout, logs to stderr, exit `1` on error) so it's friendly to scripts and agents.

It's a port of [`mercadona-cli`](https://github.com/ivorpad/mercadona-cli) to Bonpreu i Esclat — same stack (Go 1.26, uTLS, dependency-free client), same agent-first shape, adapted to a different (and entirely different-looking) API.

> Unofficial. Bonpreu has no public API. This talks to the same HTTP endpoints the website does; use at a sane request rate. Catalog reads need no login.

### Key Features

| Feature | Description |
|---------|-------------|
| **WAF-clearing reads** | Chrome TLS (JA3) fingerprint via uTLS clears the site's AWS WAF — anonymous reads work from any IP |
| **Full-text search** | `search`/`batch` with `--fresh` to drop frozen + canned, sponsored-result skipping |
| **Best value per kg/L/u** | `--cheapest` ranks results by unit price and marks the best per unit |
| **Deterministic totals** | `total` sums a basket in integer cents — exact and reproducible |
| **Product detail** | Price, brand, format, offers, and ingredients/nutrition text when present |
| **Preferred brands** | `[brands]` config picks your favourite brands (global + per-product), else the cheapest |
| **Multi-buy offers** | Flags actionable deals (2nd-unit, lot, NxM) so you can take them; `[offers]` config |
| **Brand catalogue** | `bonpreu brands` — ~3k brands shipped in-binary, `--update` to refresh; `--csv` prefixes formula-like names with a tab for spreadsheet viewing (tab stays in data) |
| **Easy auth** | `bonpreu login --from-browser chrome` lifts the session from your browser — no DevTools |
| **Authenticated cart** | `cart get/add/set/clear`, verified end-to-end against a real account |
| **Spending guard** | `--max <eur>` (also env / config) refuses a cart line over the cap before writing |
| **Claude skill** | Bundled `bonpreu-shop` skill shops from a **photo** of a basket/fridge/receipt |
| **JSON + human output** | Structured `--json`/`--toon` for agents, readable tables for people |

### Supported Outputs

Diagnostics use Go's standard `log/slog` and go to stderr, keeping
`--json`/`--toon` stdout machine-readable. Text is the default; set
`BONPREU_LOG_FORMAT=json` for structured JSON and `BONPREU_LOG_LEVEL` to
`debug`, `info` (default), `warn`, or `error`. Remote HTTP response bodies are
excluded from error strings because they are untrusted and may contain secrets.
The client accepts an optional `*slog.Logger`; without one, library calls stay
silent. Current client events include `http.retry`, `transport.fallback`,
and `security.cookie_external_host`. Retry
events carry method, status, attempt,
maximum attempts, and wait duration; events omit request URLs, bodies, cookies,
and raw error text. Add new diagnostics at the operation boundary with a stable
`event_name` and only the minimum safe fields needed to diagnose the outcome.

```text
Catalog data    Human tables, JSON (--json)
Basket total    Per-line subtotals + total (integer-cent exact)
Cart / checkout Raw JSON projection + readiness status
```

---

## Installation

### From Source (Go 1.26+)

```bash
git clone https://github.com/seifreed/bonpreu-cli.git
cd bonpreu-cli
go build -o bonpreu ./cmd/bonpreu
./bonpreu version
```

Put `bonpreu` on your `PATH` to call it from anywhere.

---

## Quick Start

```bash
# Search the catalog (anonymous, no login)
bonpreu search llet --limit 5

# Price a plain-words list in one command
printf 'pa\nllet\nous\nmantega\n' | bonpreu batch -f -

# Deterministic basket total
printf '23597 1\n00944 2\n' | bonpreu total -f -

# Product detail (price, offer, ingredients when present)
bonpreu product 01756
```

---

## Usage

### Read Core (anonymous — no login)

| Command | Description |
|---------|-------------|
| `bonpreu search <term…>` | Full-text search. `--limit N`, `--fresh` (drop *Congelats* + *Conserves*), `--cheapest` (rank by €/kg·L·u, mark the best per unit) |
| `bonpreu batch -f <file\|->` | Cheapest available hit per term (one per line; `#` comments ok), or your preferred brand when `[brands]` is set. `--brand a,b`, `--no-brands`, `--offers`, `--fresh` |
| `bonpreu total -f <file\|->` | Deterministic basket total from `<id> [qty]` lines |
| `bonpreu product <id>` | Detail: price, brand, format, offers, ingredients/nutrition |
| `bonpreu categories` | Category tree (`--depth N`); `--id <code>` lists a category's products (`--cheapest` ranks them by €/kg·L·u) |
| `bonpreu brands` | List product brands + counts from the shipped snapshot (`--csv`/`--json`); `--update` re-crawls live |

Common flags (anywhere after the subcommand): `--lang ca|es`, `--json`, `--toon` (token-oriented output, fewer tokens than JSON — for LLM/agents).

#### Price a shopping list written in plain words

`batch` bridges names → ids+prices in one command — the **cheapest available** organic hit per term (sponsored placements skipped), or your preferred brand when `[brands]` is configured:

```console
$ printf 'pa\nllet\nous\nmantega\n' | bonpreu batch -f -
• pa      → [69962] CRISTALLINO Pa de vidre Cristallino — 1.59€ (7.95€/kg)
• llet    → [23597] BONPREU Llet sencera 6x1L en cartró — 5.76€ (0.96€/L)
• ous     → [38974] BONPREU Ous frescos de gallines soltes en el galliner L — 3.20€ (3.20€)
• mantega → [54563] ASTURIANA Mantega tradicional — 3.29€ (13.16€/kg)
```

Then price a basket file (inline `#` comments allowed) — `total` sums `price × qty` in integer cents:

```console
$ printf '23597 1\n00944 2  # tomàquet cirerol\n' | bonpreu total -f -
  [23597] BONPREU Llet sencera 6x1L en cartró — 1 × 5.76€ = 5.76€
  [00944] LA COLLITA Tomàquet cirerol pera en safata de 250 g — 2 × 1.69€ = 3.38€
  total: 9.14€  (2 línies)
```

#### Find the best value per kg / L / unit

`--cheapest` ranks the results by their normalised unit price and marks the
cheapest in each unit group with `★` — so the best €/kg (or €/L, €/u) is on top, not buried by relevance:

```console
$ bonpreu search oli --cheapest
  ★ [12345] BONPREU Oli de gira-sol 1L — 2.10€ (2.10€/L)  ← millor /L
    [67890] CARBONELL Oli d'oliva 1L — 8.50€ (8.50€/L)
    [54321] Oli d'oliva verge extra 5L — 49.00€ (9.80€/L)
```

With `--json` the slice is reordered the same way (cheapest first); the marker is human-output only — agents read each product's `unitPrice` directly.

#### Find products on offer

Each product carries its `promotions` with the human label the site shows (`Abans 2,89€` = was 2.89€):

```console
$ bonpreu product 01756
[01756] LA COLLITA Tomàquet per sucar pa en capsa de 500 g
  marca: LA COLLITA
  preu: 2.29€  (4.58€/kg)
  format: 0.5kg
  oferta: Abans 2,89€
```

### Authenticated (bring your own browser session)

Bonpreu authenticates with a **cookie session** (`global_sid` + the AWS WAF clearance `aws-waf-token`). The first sign-in is browser-only (SSO/captcha); these cookies are **HttpOnly**, so the CLI can't read them from a page — it lifts them from your browser instead. Three ways, easiest first:

```bash
# 1) From your browser's cookie store — just stay logged in (recommended).
bonpreu login --from-browser chrome     # or chromium|firefox|safari|edge|brave (empty = any)
                                         # → ~/.bonpreu/session.json (0600); verifies itself

# 2) Open a browser, log in, capture the session (needs a build with the tag + Chrome):
#    go build -tags browserlogin -o bonpreu ./cmd/bonpreu
bonpreu login

# 3) Manual — import a DevTools HAR or paste the Cookie header:
#    DevTools → Network → right-click a request → "Save all as HAR with sensitive data"
#    (NOT the ⤓ button — that one is sanitized and strips the cookies)
bonpreu import-har --file compraonline.har
bonpreu set-cookie --stdin               # paste the Cookie header, then press Ctrl-D
bonpreu whoami                           # verify the session is accepted
```

`set-cookie` requires both `global_sid` and `aws-waf-token`; a cookie without
those session keys is rejected without replacing the saved session. Prefer
`--stdin` so the cookie is not placed in shell history or process arguments.

> `login --from-browser` reads the **decrypted** cookie store (on macOS Chrome this may pop a one-time Keychain prompt). The open-browser `login` uses [chromedp](https://github.com/chromedp/chromedp) and so is gated behind `-tags browserlogin` to keep the default binary dependency-free; without the tag it points you back to `--from-browser`.

| Command | Description |
|---------|-------------|
| `bonpreu cart get` | Show the active cart (`--json` for the raw response) |
| `bonpreu cart add <id> <qty>` | Add qty of a product (additive). `--max <eur>` |
| `bonpreu cart set <id> <qty>` | Set absolute qty (`0` removes). `--max <eur>` |
| `bonpreu cart clear` | Empty the cart |
| `bonpreu checkout [status]` | Checkout readiness: total, 35€ minimum, blockers |
| `bonpreu checkout addresses` | List saved delivery destinations (`--json`) |
| `bonpreu checkout slots --address <id>` | List delivery slots for a destination |
| `bonpreu checkout reserve --address <id> --slot <id>` | Reserve a delivery slot for the active cart; region comes from the session or `--region` |
| `bonpreu checkout browser` | Open the official checkout with the saved session; review and confirm there |

Cart writes resolve the short retailer id to the API's product UUID, then `POST …/apply-quantity` — the same delta-based call the website's quantity buttons make (the CLI fetches the required `X-CSRF-TOKEN` itself). `whoami` is a real check: it hits an endpoint that returns `401` for anonymous callers.

> `checkout browser` opens Bonpreu's official checkout in Chrome (requires `-tags browserlogin` and Chrome). Review the order and confirm it yourself in the page; payment authentication stays with Bonpreu and your bank. The CLI does not submit payment or report that an order was placed.

### Spending Guard (agent safety)

| Option | Description |
|--------|-------------|
| `--max <eur>` | Refuse a resulting product line over the cap, before writing; existing quantity counts |
| `BONPREU_MAX_EUR` | Same cap via environment variable |
| `[limits] max_eur` | Same cap in `~/.bonpreu/config.toml` (precedence: flag > env > config) |

### Preferred Brands

By default `batch` resolves each item to the **cheapest available** organic hit. Pin favourite brands in `~/.bonpreu/config.toml` and it selects those instead:

```toml
[brands]
preferred  = ["Hellmann's", "Asturiana"]   # global, by priority — used when present, ignored otherwise
mode       = "cheapest_among"              # among matches: cheapest (default) | "strict_priority"
on_missing = "ask"                         # per-item override missing → "ask" (skill prompts) | "cheapest"
[brands.overrides]                         # per-product brand, wins over the global list
"pastillas lavavajillas" = ["Finish"]
```

No `[brands]` section → cheapest, as before. A snapshot of every Bonpreu brand ships with the binary (`internal/branddata/brands.json`, ~3k brands) — browse it with `bonpreu brands` to pick favourites. Refresh it through a temporary file so a failed crawl cannot truncate the current snapshot: `tmp="$(mktemp)" && bonpreu brands --update --json > "$tmp" && mv "$tmp" internal/branddata/brands.json`.

### Multi-buy Offers

`batch` flags actionable Bonpreu deals (2nd-unit, lot, NxM) — structured hits carry the resolved `offerMode` and, unless ignored, `offer` + `offerLabel`; human output marks them 💡. `[offers] mode` (or `--offers`) decides the plan behavior:

```toml
[offers]
mode = "alert"   # surface the deal and ask before taking it (default, incl. when unset) | "ignore" | "accept"
```

The skill alerts on these and asks before bumping quantity (e.g. "2a unitat 50% — ¿pongo 2?"), always under `--max`.

---

## Claude Skill — Shop From a Photo

This repo bundles a Claude Code skill, **`bonpreu-shop`** (`.claude/skills/bonpreu-shop/`), that drives this CLI to build and verify a cart — including from a **photo** of a basket, fridge, receipt or handwritten list:

1. Claude reads the image into an item list and shows you what it parsed.
2. Resolves each item to a real Bonpreu product with `batch`/`search`.
3. Shows the plan with a code-computed total.
4. After your OK, fills the cart with a hard `--max` spending cap.
5. Verifies the cart and stops. It does not start checkout, reserve a delivery slot, or place a purchase.

The vision step lives in the agent, not the binary — so any provider drives the same clean primitives, no LLM SDKs baked into the CLI. Install it where your Claude reads skills (symlink or copy `.claude/skills/bonpreu-shop` into `~/.claude/skills/`); build the CLI first.

---

## Design / Reliability

The site sits behind **AWS WAF** with a JS challenge. A vanilla Go client's TLS fingerprint trips it; the CLI presents **Chrome's TLS (JA3) fingerprint via uTLS**, which keeps anonymous catalog reads on the no-challenge path — they work from any IP, no cookies.

The API is a REST surface on the same host:

```text
Search          GET /api/webproductpagews/v6/product-pages/search?q=…
Category list   GET /api/webproductpagews/v6/product-pages?retailerCategoryId=…
Category tree   GET /api/webproductpagews/v1/categories?categoryDepth=N
Product detail  Scraped from the SSR page (the detail API is WAF-gated GraphQL)
Cart read       GET  /api/cart/v1/carts/active
Cart write      POST /api/cart/v1/carts/active/apply-quantity   (X-CSRF-TOKEN + delta body)
```

The cart **write** path is IP/automation-sensitive (AWS WAF) — run it from a residential IP with a freshly exported session, not a datacenter/serverless egress.

---

## Configuration

State lives in `~/.bonpreu/` (override with `BONPREU_CONFIG_DIR`):

| File | Contents |
|------|----------|
| `config.toml` | User-managed settings; `[auth] cookie` may contain a session secret |
| `session.json` (`0600`) | Machine-managed session cache (cookie + region), written by `login`/`import-har`/`set-cookie` |

On Unix-like systems, the CLI reads and writes state only when the config directory is private
(`0700`). If an existing directory is more accessible, restrict it with `chmod 700 <directory>`.
On Windows, restrict the directory's ACL to the current user; Go's permission bits cannot verify it.
Unknown TOML keys are rejected, so a misspelling of `limits.max_eur` cannot silently fall back to a
zero spending cap.

The brand catalogue ships **in the repo/binary** (`internal/branddata/brands.json`), not under `~/.bonpreu/`.

`BONPREU_BASE_URL` can point the CLI at a mock or staging API. Session cookies are sent only to Bonpreu hosts and loopback by default. For a trusted remote HTTPS staging host, explicitly set `BONPREU_ALLOW_EXTERNAL_COOKIE_HOST=1`; the CLI still emits a security warning for that host.

---

## Status

The anonymous **read core** — `search`, `batch`, `total`, `product`, `categories`, `brands` — is implemented and **verified live** against the production site (uTLS clears the AWS WAF on every read), including `--cheapest` ranking, `[brands]` preferences, and `[offers]` flagging.

The **authenticated leg** — `login --from-browser` / `login` / `import-har` / `set-cookie`, `whoami`, `cart` (get/add/set/clear) with the `--max` guard, and checkout readiness/address/slot flows — is implemented against the live API. The cart read/write contract was **verified end-to-end** against a real account (login → add → read-back → remove, all `HTTP 200`). Final order placement and payment authentication remain unimplemented.

---

## Requirements

- Go 1.26+ (build) — see [go.mod](go.mod) for dependencies
- For the authenticated cart leg: a residential IP and a browser session (easiest: `bonpreu login --from-browser chrome`)
- Optional, for the open-browser `login`: build with `-tags browserlogin` and have Chrome installed

---

## Contributing

Contributions are welcome.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

Run the full Go quality gate before opening a PR: `make check` (formatting, vet, tests, build, golangci-lint for both build configurations, and `govulncheck`). Install `golangci-lint` v2 and `govulncheck` v1.9.0 first. The golangci configuration includes Staticcheck, gosec, errcheck, revive, ineffassign, unused, and gocritic.

---

## Support the Project

If this project is useful in your workflows, you can support development:

<a href="https://buymeacoffee.com/seifreed" target="_blank">
  <img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" height="50">
</a>

---

## License

This project is licensed under the MIT license. See [LICENSE](LICENSE).

**Attribution**
- Author: **Marc Rivero López** | [@seifreed](https://github.com/seifreed)
- Repository: [github.com/seifreed/bonpreu-cli](https://github.com/seifreed/bonpreu-cli)

---

<p align="center">
  <sub>Built for practical grocery automation and personal price tracking</sub>
</p>
