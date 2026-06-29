package main

import (
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestUnitSuffix(t *testing.T) {
	cases := []struct {
		unit, unitName, want string
	}{
		{"fop.price.per.kg", "PER_1KG", "/kg"},
		{"fop.price.per.l", "PER_1L", "/L"},
		{"fop.price.per.each", "EACH", "/u"},
		{"", "", ""},
	}
	for _, c := range cases {
		got := unitSuffix(client.UnitPrice{Unit: c.unit, UnitName: c.unitName})
		if got != c.want {
			t.Errorf("unitSuffix(%q,%q) = %q, want %q", c.unit, c.unitName, got, c.want)
		}
	}
}

func TestProductLineSponsoredAndTrim(t *testing.T) {
	p := client.Product{
		RetailerProductID: "49637",
		Name:              "  DOLCE GUSTO Cafè  ", // stray leading/trailing space
		ExternalAdvertID:  "ad-123",
		Price:             client.Money{Amount: "8.55"},
	}
	line := productLine(p)
	if strings.Contains(line, "]  DOLCE") {
		t.Errorf("name not trimmed: %q", line)
	}
	if !strings.Contains(line, "(patrocinat)") {
		t.Errorf("sponsored not flagged: %q", line)
	}
}

func TestProductLineOffer(t *testing.T) {
	p := client.Product{
		RetailerProductID: "01756",
		Name:              "Tomàquet",
		Price:             client.Money{Amount: "2.29"},
		Promotions:        []client.Promotion{{Description: "Abans 2,89€"}},
	}
	if !strings.Contains(productLine(p), "Abans 2,89€") {
		t.Error("offer description should appear in the line")
	}
}

func TestProductLineVariants(t *testing.T) {
	p := client.Product{
		RetailerProductID: "1",
		Name:              "Cafè",
		Price:             client.Money{Amount: "2.00"},
		UnitPrice:         client.UnitPrice{Price: client.Money{Amount: "2.00"}, UnitName: "PER_1KG"},
		ExternalAdvertID:  "ad",
		Available:         false,
		Promotions:        []client.Promotion{{Description: "Oferta 2x1"}},
	}
	line := productLine(p)
	for _, want := range []string{"patrocinat", "no disponible", "⟨Oferta 2x1⟩", "/kg"} {
		if !strings.Contains(line, want) {
			t.Errorf("productLine missing %q: %s", want, line)
		}
	}
}
