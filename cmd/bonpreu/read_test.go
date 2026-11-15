package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestRankByUnit(t *testing.T) {
	perKg := func(amount string) client.UnitPrice {
		return client.UnitPrice{Price: client.Money{Amount: amount}, Unit: "fop.price.per.kg", UnitName: "PER_1KG"}
	}
	perL := func(amount string) client.UnitPrice {
		return client.UnitPrice{Price: client.Money{Amount: amount}, Unit: "fop.price.per.l", UnitName: "PER_1L"}
	}
	prods := []client.Product{
		{RetailerProductID: "kg-mid", UnitPrice: perKg("9.80")},
		{RetailerProductID: "no-unit"},                            // non-comparable, sinks to bottom
		{RetailerProductID: "kg-low-a", UnitPrice: perKg("2.10")}, // tied group min
		{RetailerProductID: "l-only", UnitPrice: perL("1.50")},
		{RetailerProductID: "kg-low-b", UnitPrice: perKg("2.10")}, // tied group min
	}

	sorted, winners := rankByUnit(prods)

	wantOrder := []string{"l-only", "kg-low-a", "kg-low-b", "kg-mid", "no-unit"}
	for i, id := range wantOrder {
		if sorted[i].RetailerProductID != id {
			t.Errorf("order[%d] = %q, want %q (got %v)", i, sorted[i].RetailerProductID, id, ids(sorted))
		}
	}

	wantWinners := map[string]bool{"l-only": true, "kg-low-a": true, "kg-low-b": true}
	if len(winners) != len(wantWinners) {
		t.Fatalf("winners = %v, want %v", winners, wantWinners)
	}
	for id := range wantWinners {
		if !winners[id] {
			t.Errorf("expected %q to be a per-group winner", id)
		}
	}
}

func TestPrintProductLinesMarksWinnersAndIndentation(t *testing.T) {
	products := []client.Product{
		{RetailerProductID: "winner", Name: "A", UnitPrice: client.UnitPrice{Unit: "fop.price.per.kg"}},
		{RetailerProductID: "other", Name: "B"},
	}
	out, err := capture(t, func() error {
		printProductLines(products, map[string]bool{"winner": true}, true)
		return nil
	})
	if err != nil || !strings.Contains(out, "  ★") || !strings.Contains(out, "    ") || !strings.Contains(out, "millor /kg") {
		t.Fatalf("cheapest product output = %q, err %v", out, err)
	}
	out, err = capture(t, func() error {
		printProductLines(products[1:], nil, false)
		return nil
	})
	if err != nil || !strings.HasPrefix(out, "  ") || strings.HasPrefix(out, "    ") {
		t.Fatalf("ordinary product output = %q, err %v", out, err)
	}
}

func TestSplitBrandsTrimsAndDropsEmptyEntries(t *testing.T) {
	got := splitBrands("  Finish, ,  Somat  ,,")
	if !reflect.DeepEqual(got, []string{"Finish", "Somat"}) {
		t.Fatalf("splitBrands = %#v", got)
	}
	if got := splitBrands(" , , "); len(got) != 0 {
		t.Fatalf("empty brand list = %#v", got)
	}
}

func TestRankByUnitSkipsSponsored(t *testing.T) {
	perKg := func(amount string) client.UnitPrice {
		return client.UnitPrice{Price: client.Money{Amount: amount}, Unit: "fop.price.per.kg", UnitName: "PER_1KG"}
	}
	prods := []client.Product{
		{RetailerProductID: "ad", ExternalAdvertID: "x", UnitPrice: perKg("1.00")}, // cheapest but sponsored
		{RetailerProductID: "organic", UnitPrice: perKg("2.00")},                   // cheapest organic
	}
	_, winners := rankByUnit(prods)
	if winners["ad"] {
		t.Error("sponsored product must not be crowned cheapest")
	}
	if !winners["organic"] {
		t.Error("cheapest organic product should win")
	}
}

func TestRankByUnitKeepsUncomparableOrder(t *testing.T) {
	prods := []client.Product{{RetailerProductID: "first"}, {RetailerProductID: "second"}}
	sorted, _ := rankByUnit(prods)
	if got := ids(sorted); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("non-comparable products reordered: %v", got)
	}
}

func ids(ps []client.Product) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.RetailerProductID
	}
	return out
}

func TestCategoriesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	useServer(t, srv, false)
	_, err := capture(t, func() error { return cmdCategories([]string{"--id", "9999"}) })
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("category 404 → friendly not-found error, got %v", err)
	}
}

func TestSearchLimitTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"A","price":{"amount":"1.00"}},{"retailerProductId":"2","name":"B","price":{"amount":"2.00"}}]}]}`))
	}))
	defer srv.Close()
	useServer(t, srv, false)
	if _, err := capture(t, func() error { return cmdSearch([]string{"--limit", "1", "x"}) }); err != nil {
		t.Fatalf("search --limit truncation: %v", err)
	}
}

func TestSearchCheapest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"A","price":{"amount":"1.00"},"unitPrice":{"price":{"amount":"1.00"},"unit":"fop.price.per.kg"}},{"retailerProductId":"2","name":"B","price":{"amount":"2.00"}}]}]}`))
	}))
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdSearch([]string{"--cheapest", "x"}) })
	if err != nil || !strings.Contains(out, "★") {
		t.Fatalf("search --cheapest output = %q, err %v", out, err)
	}
}

func TestCategoriesCheapest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"A","price":{"amount":"1.00"},"unitPrice":{"price":{"amount":"1.00"},"unit":"fop.price.per.kg"}}]}]}`))
	}))
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdCategories([]string{"--id", "0301", "--cheapest"}) })
	if err != nil || !strings.Contains(out, "★") {
		t.Fatalf("categories --cheapest output = %q, err %v", out, err)
	}
}

func TestBatchBadFileAndEmpty(t *testing.T) {
	if err := cmdBatch([]string{"-f", filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Error("batch with a missing file should error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"productGroups":[]}`)) // every term returns nothing
	}))
	defer srv.Close()
	useServer(t, srv, false)
	if _, err := capture(t, func() error { return cmdBatch([]string{"zzz"}) }); err == nil {
		t.Error("batch where a term has no product should report it")
	}
}

func TestBatchPreservesSearchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	useServer(t, srv, false)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"llet"}, "ERROR: bonpreu api: HTTP 500"},
		{[]string{"--json", "llet"}, `"error": "bonpreu api: HTTP 500"`},
	} {
		out, err := capture(t, func() error { return cmdBatch(tc.args) })
		if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Fatalf("batch search error = %v; want original HTTP error", err)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("batch output should retain per-term failure %q, got %q", tc.want, out)
		}
	}
}

func TestBatchReportsResolvedOfferMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"X","brand":"B","price":{"amount":"1.00"},"promotions":[{"description":"2a unitat 50%"}]}]}]}`))
	}))
	defer srv.Close()
	useServer(t, srv, false)
	for _, tc := range []struct {
		args []string
		mode string
	}{
		{[]string{"--json", "--offers", "alert", "x"}, "alert"},
		{[]string{"--json", "--offers", "ignore", "x"}, "ignore"},
		{[]string{"--json", "--offers", "accept", "x"}, "accept"},
	} {
		out, err := capture(t, func() error { return cmdBatch(tc.args) })
		if err != nil || !strings.Contains(out, `"offerMode": "`+tc.mode+`"`) {
			t.Fatalf("batch mode %q output = %q, err %v", tc.mode, out, err)
		}
		if tc.mode == "ignore" {
			if strings.Contains(out, `"offer":`) {
				t.Fatalf("ignore mode still returned offer fields: %q", out)
			}
		} else if !strings.Contains(out, `"offer": "multibuy"`) {
			t.Fatalf("mode %q lost the actionable offer: %q", tc.mode, out)
		}
	}
}

func TestBatchWarnsForMissingBrandAndMultibuy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"A","brand":"Other","price":{"amount":"1.00"},"promotions":[{"description":"2x1"}]}]}]}`))
	}))
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdBatch([]string{"--brand", "Preferred", "x"}) })
	if err != nil || !strings.Contains(out, "marca preferida no disponible") || !strings.Contains(out, "oferta: aprofitable per quantitat") {
		t.Fatalf("batch diagnostic output = %q, err %v", out, err)
	}
}

func TestTotalBadFileAndUnpriceable(t *testing.T) {
	if err := cmdTotal([]string{"-f", filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Error("total with a missing file should error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"7","productId":"u","name":"X","price":{"amount":"NaN€"}}}</html>`))
	}))
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdTotal([]string{"7"}) })
	if err == nil || !strings.Contains(out, "ERROR") {
		t.Errorf("unpriceable line: %q err %v", out, err)
	}
}

func TestTotalRejectsAggregateOverflow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/products/")
		_, _ = fmt.Fprintf(w, `<html>window.__URQL_DATA__ = {"p":{"retailerProductId":%q,"productId":"u-%s","name":"X","price":{"amount":"50000000000000000"}}}</html>`, id, id)
	}))
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdTotal([]string{"1", "2"}) })
	if err == nil || !strings.Contains(out, "basket total exceeds the supported range") {
		t.Fatalf("cmdTotal output = %q, error = %v; want overflow error", out, err)
	}
}

// cmdProduct should print brand, unit price, pack size, promotions and detail
// blocks when present.
func TestCmdProductRichDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"u","name":"Llet","brand":"ATO","packSizeDescription":"1L","price":{"amount":"1.69","currency":"EUR"},"unitPrice":{"price":{"amount":"1.69"},"unit":"fop.price.per.l","unitName":"PER_1L"},"promotions":[{"description":"Abans 2.00"}]},"d":{"title":"Ingredients","content":"Llet sencera"}}</html>`))
	}))
	defer srv.Close()
	useServer(t, srv, false)

	out, err := capture(t, func() error { return cmdProduct([]string{"23597"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"marca: ATO", "format: 1L", "/L", "oferta:", "Ingredients", "Llet sencera"} {
		if !strings.Contains(out, want) {
			t.Errorf("product detail missing %q in:\n%s", want, out)
		}
	}
}

func TestCmdProductRejectsSurplusArgumentsBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	useServer(t, srv, false)

	for _, args := range [][]string{nil, {"23597", "unexpected"}} {
		if _, err := capture(t, func() error { return cmdProduct(args) }); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("product %v error = %v, want usage error", args, err)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid product command made %d requests, want none", got)
	}
}

func TestCmdTotalFailedLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // every product lookup 404s
	}))
	defer srv.Close()
	useServer(t, srv, false)
	// One id that can't be priced → reported as ERROR and a non-nil final error.
	out, err := capture(t, func() error { return cmdTotal([]string{"99999"}) })
	if err == nil {
		t.Error("expected an error when a line cannot be priced")
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("failed line should be marked ERROR: %q", out)
	}
	_ = client.Product{}
}

func TestCmdTotalJSONReturnsFailureForUnpricedLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdTotal([]string{"--json", "99999"}) })
	if err == nil || !strings.Contains(err.Error(), "could not be priced") {
		t.Fatalf("total --json error = %v; want incomplete-total error", err)
	}
	if !strings.Contains(out, `"complete": false`) || !strings.Contains(out, `"error": "bonpreu api: HTTP 500"`) {
		t.Fatalf("total --json output did not preserve failed line: %q", out)
	}
}
