---
name: bonpreu-shop
description: >-
  Do the grocery shop at Bonpreu i Esclat online (compraonline.bonpreuesclat.cat) by driving the
  local `bonpreu` CLI: turn a shopping list — or a PHOTO of a basket, fridge, receipt or handwritten
  list — into real priced Bonpreu products, confirm them, and fill the cart with a hard spending cap.
  Use whenever the user wants to shop, price a grocery list, build a cart, or "do the Bonpreu shop" —
  including when they just paste a list or drop an image and mention Bonpreu/Esclat. Spanish/Catalan
  phrasings: "hazme la compra del Bonpreu", "fes la compra", "añade X al carrito", "¿cuánto cuesta
  esta lista en Bonpreu?", "compra estos productos", "mira esta foto de la cesta y pídelo". English:
  "do my Bonpreu shop", "price this list at Bonpreu", "fill my Bonpreu cart", "shop this fridge photo
  at Bonpreu". Always confirm the resolved products before touching the cart, always cap spend with
  --max, and never claim an order was placed — this CLI prepares the cart but does NOT submit orders.
  Do NOT use for other supermarkets (Mercadona, Carrefour, Lidl) — Bonpreu/Esclat only.
---

# Bonpreu shop

Turn a shopping list — or a photo of one — into a real Bonpreu i Esclat cart by driving the
`bonpreu` CLI: read the image, resolve each item to a real product with its price, confirm with the
user, and fill the cart with a spending cap. The CLI talks to the same HTTP endpoints
`compraonline.bonpreuesclat.cat` uses; this skill is the playbook for using it safely on a real account.

## Three things that are non-negotiable (read first)

This touches a real account and real money, so:

1. **Confirm before touching the cart.** Reading, searching and pricing are free and side-effect-free
   — do them freely. But before the *first* `cart add`/`cart set`, show the user the resolved plan
   (each list/photo item → the exact product you picked: `[id] name — size — price`) and wait for an
   explicit OK. Product matching is fuzzy (and a photo adds a second fuzzy step), so this is where the
   user catches a wrong brand, size or quantity — cheap now, annoying after the cart is full.
2. **Always cap spend with `--max`.** Put a hard euro ceiling on every cart write so a wrong match or a
   fat-fingered quantity can't run up the order. Use the user's stated budget, else the agreed plan
   total rounded up with a little margin. A line over the cap fails with `error: BUDGET EXCEEDED …` and
   a non-zero exit — treat that as **stop-and-report**, don't raise the cap unless the user does.
3. **This CLI does NOT place orders.** There is no `submit`. The skill prepares the cart and reports
   checkout readiness (`bonpreu checkout`), but the user finishes the order themselves in the browser
   (slot + payment). Never tell the user you "placed the order" — you can't. Say the cart is ready and
   point them to the web to book a slot and pay.

## Locate the binary

Run `bonpreu` from `PATH`. If it isn't there, build it from the repo (Go 1.26+):

```bash
go build -o bonpreu ./cmd/bonpreu   # in the bonpreu-cli checkout; or put the binary on PATH
bonpreu version
```

Catalan is the default language (`--lang es` for Spanish). Reads need no login.

## Authenticate (bring-your-own session)

Search/price/categories need **no login**. Cart needs a session. Bonpreu authenticates with a
**cookie session** (the first login is browser-only — SSO/captcha — so the CLI can't log in for you;
the cookies are HttpOnly, so they can't be read from a page). The user logs in once at
`compraonline.bonpreuesclat.cat` in their **own browser** (residential IP — the write path is
bot-sensitive behind AWS WAF), then pick the easiest way to hand the session to the CLI:

1. **`bonpreu login --from-browser chrome`** — the easy path, prefer it. Reads the session straight
   from the browser's cookie store (`chrome`|`chromium`|`firefox`|`safari`|`edge`|`brave`; empty = any
   installed). No DevTools, no files. On macOS Chrome it may pop a one-time Keychain prompt. The
   command self-verifies and prints `ok — authenticated (region …)`.
2. **`bonpreu login`** — opens a browser, the user logs in, the CLI captures the session. Only in a
   build with `-tags browserlogin` (+ Chrome installed); otherwise it points back to `--from-browser`.
3. **Manual fallback** — DevTools → Network → right-click a request → **"Save all as HAR with sensitive
   data"** (NOT the ⤓ button — that exports a *sanitized* HAR with the Cookie headers stripped, which
   `import-har` will reject), then `bonpreu import-har --file compraonline.har`. Or paste the `Cookie:`
   header into `bonpreu set-cookie '<cookie>'` (or `--stdin`).

Always confirm with `bonpreu whoami`. The `aws-waf-token` is **short-lived and rotates**, so a cached
session expires after a while: if `whoami` says **not authenticated**, just re-run
`bonpreu login --from-browser chrome` to lift a fresh token.

**Never echo the cookie back to the user.** Feed it via `--from-browser`, the HAR/file or `--stdin`,
never print it.

## The photo → cart workflow (the headline)

When the user drops an **image** (a basket, fridge shelf, a receipt, a handwritten or printed list):

### 1. Read the image into a clean item list — yourself

You have vision. Extract the grocery items from the photo into a plain list of **names + quantities**,
in the user's language. Be literal about what you can see; don't invent items. For a receipt/printed
list, transcribe it; for a fridge/basket photo, name the products you recognise (e.g. "llet entera",
"ous", "tomàquets", "iogurt natural"). If parts are ambiguous or unreadable, list your best guess and
flag it — you'll confirm everything before buying anyway.

**Show the user what you parsed** and let them correct it *before* you price anything. This is the
photo-specific checkpoint: vision can misread a label or miss an item at the back of the shelf.

### 2. Resolve names → real products (free, no login)

Price the whole parsed list in one command with `batch` — one term per line. It picks the **cheapest**
organic hit per term (sponsored placements skipped), or your **preferred brand** when `[brands]` is
configured (see "Brand preferences"):

```bash
printf 'llet entera\nous\ntomàquet\niogurt natural\n' | bonpreu batch -f -
```

`batch` is fast but a rough match. So:
- For anything the user was specific about (brand, size, "el més barat"), or where the top hit looks
  off, run `bonpreu search "<term>" --limit 5` and pick deliberately.
- `--fresh` drops frozen (*Congelats*) + canned (*Conserves*) so the fresh item surfaces:
  `bonpreu search tonyina --fresh --limit 5`.
- Use `--json` when you need to parse ids/prices reliably (or `--toon` for the same fields at fewer tokens).

**Watch as you match:** pack-vs-unit (a 6-pack is qty 1, not 6), weight items price per kg and take
fractional quantities (`0.5`), and sanity-check absurd amounts from a misread photo ("200 ous" → "20").
`batch`'s fuzzy match **fails loudly and often** on uncommon terms — it once matched *sake* → a
raclette cheese, *eneldo* → Pringles, *mejorana* → strawberry yogurt. Eyeball every hit; re-`search`
anything that looks wrong, and prefer Catalan terms (the catalog is Catalan-first).

**Mark what doesn't exist or got substituted.** Some items simply aren't stocked (e.g. sake, mirin,
mejorana, estragón). Don't force a bad match: keep an explicit **"not available — buy elsewhere"** list,
and flag every substitution (⚠️ peeled vs whole prawns, white miso vs red) in the plan so the user
vetoes or sources it himself.

### 3. Present the plan and get the OK (Gate 1)

Show a compact table: each item → `[id] name — size — price` and a line quantity, plus the
not-available / substitution notes. **Compute the total with code, not by hand.**

For a **small list** (a handful of lines), feed the resolved `<id> <qty>` lines to `bonpreu total`:

```bash
printf '23597 1\n38974 1\n00944 2\n' | bonpreu total -f -   # per-line subtotals + total
```

For a **large list (≈20+ items), do NOT use `bonpreu total`** — it re-fetches every product serially
(each cart/detail lookup scrapes a page) and will time out on a big basket. You already have every
price from the `batch --json` pass: **sum those in code** (`price × qty`, in cents) instead. Only fall
back to `total` for the few lines you re-priced via `search`.

Surface every assumption (brand, size, fresh/frozen, a guessed photo item, a substitution) so the user
can veto it. Edit and re-total until they're happy. Only then touch the cart.

### 4. Fill the cart (after the OK)

Add each confirmed line with a spending cap. `cart add <id> <qty>` is additive; `cart set <id> <qty>`
sets the **absolute** quantity (`0` removes). `--max` is a **per-line** cap, not a basket total — it
refuses a single line over the ceiling; it does not stop the running sum:

```bash
bonpreu cart add 23597 1 --max 60
bonpreu cart get                      # verify lines + total
```

**For a big list, fill with `cart set`, not `cart add`.** Each write is slow (it resolves the short id
to a UUID by scraping, then applies the delta), so a long fill can outlast a shell timeout — and
re-running `cart add` after a partial fill **double-adds**. `cart set <id> <qty>` is **idempotent**:
re-run the whole list and already-correct lines are no-ops (`already at target quantity`), missing ones
get added — so it resumes cleanly after any interruption. Run it as a background job for long lists.
To start from a known-empty cart, `bonpreu cart clear` first (check `cart get` so you don't wipe items
the user added himself). After writing, `cart get` and reconcile against the plan.

> The cart backend can lag a write by a beat — if a `cart get` right after a write looks stale, re-run
> it; it converges in a second or two. Don't infer a bug. (Two short ids can also resolve to the same
> product UUID and merge into one line — a smaller line count than items isn't a bug.)

### 5. Hand off for checkout (the CLI stops here)

```bash
bonpreu checkout              # readiness: total, 35€ minimum, what's blocking (min/slot/empty)
```

The **minimum order is 35€** — if `checkout` says `no s'arriba al mínim`, tell the user and offer to
add items. The CLI is **read-only at checkout**: it can't book a slot or pay. Tell the user the cart is
ready and they finish in the browser (choose a delivery slot + pay). Never claim the order was placed.

`bonpreu checkout addresses` lists saved delivery destinations and `checkout slots --address <id>` the
slots for one. Note `slots` often returns **"no slots offered right now"** even for a valid address —
the CLI hits the exact endpoint the site does, and Bonpreu's slot service frequently returns nothing
(the website shows a "slot service down" card for the same case). It's not a bad id or a CLI bug;
slot-booking lives in the browser anyway, so just hand off.

## Plain lists, recipes, and clarifying

Not every request is a photo. For a **pasted list** ("arròs, pollastre, 6 ous") resolve each item — same
steps 2-5. For a **dish/recipe** ("fes-me una paella per a 4"), first turn it into ingredients scaled to
a headcount, and **ask the few things that genuinely move the basket** before pricing: how many people,
allergies (a marisco/gluten/lactosa miss is a health risk, not a wrong brand), and recipe variant when
versions differ. Prefer the **AskUserQuestion** tool — one axis per question, 2-4 options, sensible
default first, in the user's language. Don't silently invent a consequential value (headcount, allergen).
Mark safe defaults in the plan so the user can veto them.

### Recipe-app / multi-recipe lists (consolidate before pricing)

A list exported from a recipe app (e.g. Thermomix "Lista de la compra") is the **union of many
recipes' ingredients**. Before resolving, clean it — this is a real step, not an afterthought:

- **Merge duplicates across recipes** — three "sal", "pimentón dulce" ×3, repeated "eneldo/pimienta" →
  one line each.
- **Recipe amounts → store formats** — you don't buy 50 g of butter or 440 g of flour; buy a pack
  (1 butter, a 1 kg bag). Cucharadas/cucharaditas/pellizcos of a spice → **one jar**.
- **Drop non-products** — tap water, "al gusto" items already implied.

Two questions move this basket more than anything else — **ask them with AskUserQuestion before pricing
~50 items**, sensible default first:

1. **Pantry staples** (salt, pepper, dried spices, flour, sugar, oil, vinegar): include, or assume the
   user already has them? (default: assume owned — a recipe-union dump otherwise buys 15 spice jars).
2. **Specialty / hard-to-find** (sake, mirin, miso, sriracha, rice vinegar, food colouring): try and
   flag what's missing, or skip? (default: try and mark the not-available ones — see step 2).

Then resolve → plan → fill as usual. Expect a large fill, so use `cart set` (step 4) and price by
summing the `batch --json` figures, not `bonpreu total` (step 3).

## Brand preferences

The user can pin favourite brands in `~/.bonpreu/config.toml` `[brands]` — a global
priority-ordered `preferred` list, plus per-term `[brands.overrides]` (`"pastillas" = ["Finish"]`).
`batch` (and `--brand a,b`) **applies these automatically**; you don't reimplement the logic — you
just read the result and handle the one case that needs a human:

- Each `batch --json` hit carries `brandMatch`: `preferred`/`override` (got a wanted brand) or `off`
  (no preference applied) → nothing to do. **`none`** means an *explicit* per-term override (or
  `--brand`) brand wasn't stocked.
- On `none`, **ask before substituting** (this is the wife's point): show the cheapest/top hit you'd
  otherwise use and offer alternatives, via **AskUserQuestion** (e.g. "Finish no está — ¿la más barata
  (Bonpreu 2,10€), otra marca, o lo salto?"). Only add to the cart after the user picks. Respect
  `on_missing = "cheapest"` if set (auto-fallback, no prompt) — but in an interactive shop, default to asking.
- A global-favourite simply not making a product (Hellmann's doesn't make milk) is **not** a `none` and
  needs no prompt — it's `off`, pick normally.
- No `[brands]` config → behave exactly as before: cheapest/top organic hit.

To help the user build the list, `bonpreu brands` crawls the catalogue into a brand menu (slow,
one-time). If the user names a brand mid-shop ("la mayonesa siempre Hellmann's"), offer to add it to
`[brands]` so it sticks next time.

## Offers (multi-buy deals)

Bonpreu pushes quantity deals — "2a unitat 50%", "Lot estalvi a 7,75€/lot", "3x2". `batch` flags the
*actionable* ones: each `--json` hit gets `offer: "multibuy"` (+ `offerLabel` with the text) when the
chosen product has a buy-more deal (a was-price or flat % discount is `"price"`, not flagged; human
output marks multibuy with 💡). Behaviour is set by `[offers] mode` in config (or `--offers`):

- **`alert`** (default, incl. when unset) — surface the deal and **ask** before acting. At Gate 1, for
  each `multibuy` item, tell the user the deal and ask if they want to take it (e.g. "Carn picada tiene
  'Lot estalvi 7,75€/lot' — ¿compro el lote?" / "2a unitat 50% — ¿pongo 2?"). Only bump quantity after
  an OK, and keep it under `--max`.
- **`ignore`** — don't surface or mention offers.
- **`accept`** — default to taking the deal in the plan (e.g. qty 2 for a 2nd-unit offer), still shown
  in the Gate-1 plan so the user can veto, still capped by `--max`.

Don't infer the deal's exact quantity from the label blindly — "2a unitat 50%" → 2, a lot → ask how
many; when unsure, ask. Never let an offer push spend past the cap.

## Reference

- `references/cli-reference.md` — every command, its flags, and the `--json` field shapes
  (product, cart line, checkout status, `brandMatch`, `offer`) for precise parsing.

## Why no all-in-one "shop the photo" script

This skill drives the CLI step by step on purpose: the confirm-before-cart gate and the spending cap
need a human in the loop, and matching a photo to real products needs judgment. Keep the control flow
here, in the conversation, where the user can steer it.
