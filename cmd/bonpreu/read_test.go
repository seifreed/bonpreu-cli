package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
