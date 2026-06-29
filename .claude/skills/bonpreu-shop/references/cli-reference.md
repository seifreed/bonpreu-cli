# `bonpreu` CLI reference

Every command, its flags, and the `--json` shapes. Data → stdout, logs/errors → stderr. Exit codes:
`0` ok, `1` runtime error, `2` usage / unknown command. Common flags (anywhere after the subcommand):
`--lang ca|es`, `--json` (raw JSON), `--toon` (TOON — same shapes as `--json`, fewer tokens; prefer it
when an agent parses the output).

## Read commands (anonymous, no login)

| Command | What it does |
|---|---|
| `bonpreu search <term…>` | Full-text search. `--limit N`, `--fresh` (drop frozen *Congelats* + canned *Conserves*), `--cheapest` (rank by unit price €/kg·€/L·€/u and mark the best per unit with ★). |
| `bonpreu batch -f <file\|->` | One hit per term (one term per line; `#` comments ok): the **cheapest** organic hit, or your preferred brand if `[brands]` is configured. `--fresh`; `--brand a,b` prefers brands for this run; `--no-brands` ignores config. |
| `bonpreu total -f <file\|->` | Deterministic basket total from `<id> [qty]` lines, summed in integer cents. |
| `bonpreu product <id>` | Detail: price, brand, format, offers, and ingredients/nutrition text when present. |
| `bonpreu categories` | Category tree (`--depth N`). `--id <retailerCategoryId>` lists that category's products (`--limit N`; `--cheapest` ranks them by unit price and marks the best per unit). |
| `bonpreu brands` | List product brands + counts from the **shipped snapshot** (instant, no network; `--csv`/`--json`). The menu for building `[brands]` favourites. `--update` re-crawls live and prints fresh JSON (redirect into `internal/branddata/brands.json` + rebuild to refresh). |

### Product / search line shape (`--json`)

```json
{
  "retailerProductId": "23597",          // the short id you pass to cart/total
  "productId": "….uuid",                 // stable UUID (the cart uses this internally)
  "name": "BONPREU Llet sencera 6x1L en cartró",
  "brand": "BONPREU",
  "packSizeDescription": "6x1L",
  "price": { "amount": "5.76", "currency": "EUR" },
  "unitPrice": { "price": { "amount": "0.96" }, "unitName": "PER_1L" },   // €/kg, €/L, EACH
  "available": true,
  "promotions": [ { "description": "Abans 6,49€", "type": "OFFER" } ],     // present only on offers
  "externalAdvertId": "…"                // present only on sponsored placements (batch skips these)
}
```

`total --json` → `{ "lines": [...], "total": "13.13", "count": 2, "complete": true }`.

`batch --json` hits add `brandMatch` + `consideredBrands` when brand prefs apply:

```json
{ "term": "maionesa", "product": { … },
  "brandMatch": "preferred",          // preferred (global) | override (per-term/--brand) | none | off
  "consideredBrands": ["Hellmann's"] }
```

`none` = an explicit override/`--brand` brand wasn't stocked → **ask the user before substituting**
(see SKILL "Brand preferences"). `off`/`preferred`/`override` need no prompt.

Hits also carry `offer` + `offerLabel` when the chosen product has a promotion: `"multibuy"` (a
buy-more deal — 2nd-unit, lot, NxM — worth acting on) or `"price"` (was-price / flat discount, info).
Controlled by `[offers] mode` (`alert` default | `ignore` | `accept`) or `--offers`; `ignore` omits the
fields. See SKILL "Offers".

## Authenticated commands (need a session)

Get a session the easy way with `login --from-browser` (no DevTools); `import-har`/`set-cookie` are the
manual fallback. The `aws-waf-token` rotates, so re-run `login --from-browser` when `whoami` fails.

| Command | What it does |
|---|---|
| `bonpreu login --from-browser <name>` | **Easiest.** Lift the session from a browser's cookie store: `chrome`\|`chromium`\|`firefox`\|`safari`\|`edge`\|`brave` (empty = any). Self-verifies. |
| `bonpreu login` | Open a browser, log in, capture the session. Only with `-tags browserlogin` (+ Chrome at runtime); else points to `--from-browser`. |
| `bonpreu import-har --file <har\|->` | Extract the session cookie + region from a DevTools HAR. Use **"Save all as HAR with sensitive data"** — the plain ⤓ export is sanitized (no Cookie headers) and is rejected. |
| `bonpreu set-cookie '<cookie>'` | Seed a raw Cookie header manually (`--stdin`). |
| `bonpreu whoami` | Verify the session is accepted (real check — 401 endpoint). |
| `bonpreu cart get` | Show the active cart. `--json` for the raw response. |
| `bonpreu cart add <id> <qty>` | Add qty of a product (additive). `--max <eur>`. |
| `bonpreu cart set <id> <qty>` | Set absolute qty (`0` removes). `--max <eur>`. |
| `bonpreu cart clear` | Empty the cart. |
| `bonpreu checkout [status]` | Checkout readiness: total, 35€ minimum, blockers. |
| `bonpreu checkout addresses` | List saved delivery destinations (`--json`). |
| `bonpreu checkout slots --address <id>` | List delivery slots for a destination (`--json`). Often returns "no slots offered right now" even for a valid id — Bonpreu's slot service, not a CLI bug; book slots in the browser. |

### Cart line shape (`cart get --json` → `items[]`)

The cart carries `productId` + `quantity` + prices, **but no product name** (resolve via the catalog
if you need one):

```json
{
  "productId": "….uuid",
  "quantity": 1,
  "price": { "amount": "4.99" },
  "finalPrice": { "amount": "4.99" },
  "totalPrices": { "finalPrice": { "amount": "4.99" } }   // line total
}
```

Cart total is at `totals.itemPriceAfterPromos.amount`.

### Checkout status shape (`checkout --json` → `activeCheckoutGroup`)

```json
{
  "canCheckout": false,
  "minimumCheckoutThreshold": { "amount": "35.00" },
  "checkoutRestrictions": ["NOT_REACHED_THRESHOLD", "MISSING_SLOT", "EMPTY_BASKET"]
}
```

`NOT_REACHED_THRESHOLD` = under 35€ · `MISSING_SLOT` = no delivery slot booked · `EMPTY_BASKET` = empty.

## Spending guard

`--max <eur>` on any cart write refuses a line over the cap (also `BONPREU_MAX_EUR` env, or
`[limits] max_eur` in `~/.bonpreu/config.toml`; precedence flag > env > config; `0`/unset = no limit).

## Notes

- **No order placement.** There is no `submit`. The CLI prepares the cart and reports readiness; the
  user books a slot and pays in the browser.
- **Per-region prices.** The region comes from the imported session; reads use a default region.
- **Bot-sensitivity.** Reads work from any IP (uTLS clears the WAF). The cart **write** path is
  bot-sensitive — run it from a residential IP with a freshly exported session.
- **Config / state.** `~/.bonpreu/config.toml` (`[defaults] region_id`/`lang`, `[limits] max_eur`,
  `[auth] cookie`, `[brands]` favourites) and `session.json` (machine-managed cookie + region).
  Override dir with `BONPREU_CONFIG_DIR`.
- **Brand prefs (`[brands]`).** `preferred = [...]` global favourites (priority order, used when one
  appears in a term's results, ignored otherwise); `mode = "cheapest_among"` (default) | `"strict_priority"`;
  `on_missing = "ask"` (default) | `"cheapest"`; `[brands.overrides]` maps a term/category to its own
  ordered brand list (`"pastillas" = ["Finish"]`), winning over the global list. Empty = pick cheapest as before.
