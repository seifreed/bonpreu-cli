package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func TestPageSizeClamp(t *testing.T) {
	cases := map[int]int{0: defaultPageMax, 10: 10, 300: 300, 301: 300, 5000: 300}
	for in, want := range cases {
		if got, err := pageSize(in); err != nil || got != want {
			t.Errorf("pageSize(%d) = %d, want %d", in, got, want)
		}
	}
	if _, err := pageSize(-5); err == nil {
		t.Fatal("negative page size should fail")
	}
}

func TestCatalogMethodsRejectNegativeLimitsBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cl := New()
	cl.BaseURL = srv.URL
	cl.HTTP = srv.Client()

	if _, err := cl.Search("milk", -1); err == nil {
		t.Fatal("search should reject a negative limit")
	}
	if _, err := cl.CategoryProducts("0301", -1); err == nil {
		t.Fatal("category products should reject a negative limit")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid limits made %d network requests, want none", got)
	}
}

func TestIsCategoryUUID(t *testing.T) {
	if !isCategoryUUID("130716f2-795a-4f0b-ad39-b449817921b3") {
		t.Error("UUID should be detected")
	}
	if isCategoryUUID("0301") {
		t.Error("short retailer code is not a UUID")
	}
}

func TestProductPageProducts(t *testing.T) {
	pp := ProductPage{ProductGroups: []ProductGroup{
		{DecoratedProducts: []Product{{RetailerProductID: "a"}}},
		{DecoratedProducts: []Product{{RetailerProductID: "b"}, {RetailerProductID: "c"}}},
	}}
	got := pp.Products()
	if len(got) != 3 || got[0].RetailerProductID != "a" || got[2].RetailerProductID != "c" {
		t.Errorf("Products() flatten wrong: %+v", got)
	}
}

func TestCSRFRegex(t *testing.T) {
	html := `...,"session":{"csrf":{"token":"e75bdde8-636a-43c1-b18a-968ccb68e9d7"},"features":...`
	m := csrfRE.FindStringSubmatch(html)
	if m == nil || m[1] != "e75bdde8-636a-43c1-b18a-968ccb68e9d7" {
		t.Fatalf("csrf token not extracted: %v", m)
	}
}

func TestSearchTruncatesByRune(t *testing.T) {
	// 60 'é' (2 bytes each) — a byte cut at 50 would split a rune; a rune cut
	// keeps 50 whole runes and stays valid UTF-8.
	term := strings.Repeat("é", 60)
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		w.Write([]byte(`{"productGroups":[]}`))
	}))
	defer srv.Close()
	cl := testClient(srv)
	if _, err := cl.Search(term, 0); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !utf8.ValidString(gotQ) {
		t.Errorf("query is not valid UTF-8: %q", gotQ)
	}
	if n := utf8.RuneCountInString(gotQ); n != searchTermMax {
		t.Errorf("rune count = %d, want %d", n, searchTermMax)
	}
}

func TestCategoryProductsShortAndUUID(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"Aigua"}]}]}`))
	}))
	defer srv.Close()
	cl := testClient(srv)

	if _, err := cl.CategoryProducts("0301", 10); err != nil {
		t.Fatalf("short id: %v", err)
	}
	if !strings.Contains(gotQuery, "retailerCategoryId=0301") {
		t.Errorf("short id query = %q", gotQuery)
	}
	if _, err := cl.CategoryProducts("130716f2-795a-4f0b-ad39-b449817921b3", 0); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if !strings.Contains(gotQuery, "isRetailerCategoryId=false") {
		t.Errorf("uuid query = %q", gotQuery)
	}
}

func TestCatalogMethodsRejectMissingOrNullProductGroups(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"productGroups":null}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			cl := testClient(srv)
			if _, err := cl.Search("milk", 0); err == nil || !strings.Contains(err.Error(), "productGroups") {
				t.Errorf("Search response %s error = %v; want shape error", body, err)
			}
			if _, err := cl.CategoryProducts("0301", 0); err == nil || !strings.Contains(err.Error(), "productGroups") {
				t.Errorf("CategoryProducts response %s error = %v; want shape error", body, err)
			}
		})
	}
}

func TestCategories(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"retailerCategoryId":"0301","name":"Begudes"}]`))
	}))
	defer srv.Close()
	cats, err := testClient(srv).Categories(0) // 0 clamps to depth 1
	if err != nil || len(cats) != 1 || cats[0].Name != "Begudes" {
		t.Fatalf("Categories = %+v, err %v", cats, err)
	}
}

func TestCategoriesRejectsNullResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`null`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).Categories(1); err == nil || !strings.Contains(err.Error(), "categories") {
		t.Fatalf("Categories null response error = %v; want shape error", err)
	}
}

func TestCategoriesRejectsNegativeDepthBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).Categories(-1); err == nil {
		t.Fatal("negative category depth should fail")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("negative category depth made %d network requests, want none", got)
	}
}

func TestSponsored(t *testing.T) {
	if !(Product{ExternalAdvertID: "ad"}).Sponsored() {
		t.Error("ad product should be sponsored")
	}
	if (Product{}).Sponsored() {
		t.Error("plain product is not sponsored")
	}
}
