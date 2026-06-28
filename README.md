# bonpreu

Unofficial, agent-friendly CLI for `compraonline.bonpreuesclat.cat` (Bonpreu i
Esclat online) — search the catalog, read prices, price a basket, and browse the
category tree. Single static Go binary, no runtime deps, structured `--json`
output for programmatic/agent use.

> Unofficial. Bonpreu has no public API. This talks to the same HTTP endpoints
> the website does; use at a sane request rate. Catalog reads need no login.

This is a port of [`mercadona-cli`](https://github.com/ivorpad/mercadona-cli) to
Bonpreu i Esclat — same stack (Go 1.26, uTLS, dependency-free client), same
agent-first shape, adapted to a different (and entirely different-looking) API.

## Install

**From source** (Go 1.26+):

```bash
go build -o bonpreu ./cmd/bonpreu
```

## Commands

### Read core (anonymous — no login)

```bash
bonpreu search llet                       # full-text product search
bonpreu search --limit 5 --json mantega   # structured output for agents
bonpreu search mejillon --fresh           # drop frozen (Congelats) + canned (Conserves)
bonpreu batch -f lista.txt                # top hit per term, one command
printf 'pa\nllet\nous\n' | bonpreu batch -f -
bonpreu total -f cistella.txt             # deterministic basket total (summed in code)
bonpreu product 49637                      # detail, price, ingredients/nutrition when present
bonpreu categories                         # category tree
bonpreu categories --id 0301 --json        # one category's products (raw JSON)
```

Common flags (may go anywhere after the (sub)command): `--lang ca|es`, `--json`.
Data goes to **stdout**, logs/errors to **stderr**, exit code `1` on error —
friendly to scripts and agents.

### Price a shopping list written in plain words

`batch` bridges names → ids+prices in one command — the top *organic* hit per
term (sponsored placements are skipped):

```console
$ printf 'pa\nllet\nous\nmantega\n' | bonpreu batch -f -
• pa      → [69962] CRISTALLINO Pa de vidre Cristallino — 1.59€ (7.95€/kg)
• llet    → [23597] BONPREU Llet sencera 6x1L en cartró — 5.76€ (0.96€/L)
• ous     → [38974] BONPREU Ous frescos de gallines soltes en el galliner L — 3.20€ (3.20€)
• mantega → [54563] ASTURIANA Mantega tradicional — 3.29€ (13.16€/kg)
```

Put your picks in a basket file (inline `#` comments allowed) and price it —
`total` sums `price × qty` in integer cents (exact; fractional quantities work
for weight items):

```console
$ printf '49637 1\n01756 2  # tomàquet per sucar\n' | bonpreu total -f -
  [49637] DOLCE GUSTO Càpsules de cafè Espresso Intens — 1 × 8.55€ = 8.55€
  [01756] LA COLLITA Tomàquet per sucar pa en capsa de 500 g — 2 × 2.29€ = 4.58€
  total: 13.13€  (2 línies)
```

### Find products on offer

Each product carries its `promotions`, with the human label the site shows
(`Abans 2,89€` = was 2.89€), so `product` and `--json` surface genuine drops:

```console
$ bonpreu product 01756
[01756] LA COLLITA Tomàquet per sucar pa en capsa de 500 g
  marca: LA COLLITA
  preu: 2.29€  (4.58€/kg)
  format: 0.5kg
  oferta: Abans 2,89€
```

## Design / reliability

The site sits behind **AWS WAF** with a JS challenge. A vanilla Go client's TLS
fingerprint trips it; the CLI presents **Chrome's TLS (JA3) fingerprint via
uTLS**, which keeps the anonymous catalog reads on the no-challenge path — they
work from any IP, no cookies.

The API is a REST surface on the same host (`/api/webproductpagews/…`,
`/api/search/…`):

- **Search** → `GET /api/webproductpagews/v6/product-pages/search?q=…`
- **Category listing** → `GET /api/webproductpagews/v6/product-pages?retailerCategoryId=…`
- **Category tree** → `GET /api/webproductpagews/v1/categories?categoryDepth=N`
- **Product detail** is GraphQL behind the WAF challenge, so `product` scrapes the
  product node out of the server-rendered page (`/products/<id>`) instead — same
  uTLS path, still anonymous.

## Config

State lives in `~/.bonpreu/` (override with `BONPREU_CONFIG_DIR`):

- `config.toml` (`0600`): `[defaults] region_id`/`lang`/`postal_code`,
  `[limits] max_eur`, and (for the future authenticated leg) `[auth] cookie`.

## Status

The anonymous **read core** — `search`, `batch`, `total`, `product`,
`categories` — is implemented and verified live against the production site.
The authenticated leg (cart/checkout via an imported browser session) is the
next milestone; the config and client already carry the session-cookie plumbing
it needs.
