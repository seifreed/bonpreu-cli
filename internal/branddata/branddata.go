// Package branddata ships a snapshot of Bonpreu's product brands (distinct brand
// names with product counts), crawled from the catalogue. It's embedded in the
// binary so `bonpreu brands` works instantly with no network; refresh it with
// `bonpreu brands --update` (see cmd/bonpreu).
package branddata

import _ "embed"

// JSON is the embedded brand catalogue: a JSON array of {"brand","count"} objects,
// ordered by count desc. Regenerate with `bonpreu brands --update`.
//
//go:embed brands.json
var JSON []byte
