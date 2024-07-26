package service

import (
	"testing"

	menuv1 "github.com/ramisoul84/kfc-userapi/gen/menu/v1"
	"github.com/ramisoul84/kfc-userapi/internal/domain"
)

func testMenu() *menuv1.GetEffectiveMenuResponse {
	return &menuv1.GetEffectiveMenuResponse{
		Currency: "RUB",
		Categories: []*menuv1.Category{{
			Items: []*menuv1.Item{
				{Id: "zinger", Name: "Zinger", ProductCode: "Z", FinalPrice: 299, IsAvailable: true},
				{Id: "sold-out", Name: "Wings", FinalPrice: 259, IsAvailable: false},
				{Id: "cola", Name: "Cola", FinalPrice: 109, IsAvailable: true, Variations: []*menuv1.Variation{
					{Id: "s", Name: "0.3", FinalPrice: 109},
					{Id: "m", Name: "0.4", FinalPrice: 139, IsDefault: true},
				}},
			},
		}},
	}
}

func TestPriceCartUsesMenuPrices(t *testing.T) {
	lines, err := priceCart(testMenu(), []CartItem{
		{ItemID: "zinger", Quantity: 2},
		{ItemID: "cola", VariationID: "s", Quantity: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].UnitPrice != 29900 || lines[0].Quantity != 2 {
		t.Fatalf("zinger priced wrong: %+v", lines[0])
	}
	if lines[1].UnitPrice != 10900 || lines[1].VariationName != "0.3" {
		t.Fatalf("cola 0.3 priced wrong: %+v", lines[1])
	}
}

// Without an explicit option the default variation is charged, which is
// what kiosks and the website show.
func TestPriceCartDefaultsToDefaultVariation(t *testing.T) {
	lines, err := priceCart(testMenu(), []CartItem{{ItemID: "cola", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].UnitPrice != 13900 || lines[0].VariationId != "m" {
		t.Fatalf("expected default 0.4 at 139 ₽, got %+v", lines[0])
	}
}

func TestPriceCartRejectsBadInput(t *testing.T) {
	cases := map[string][]CartItem{
		"unknown item":     {{ItemID: "nope", Quantity: 1}},
		"sold out":         {{ItemID: "sold-out", Quantity: 1}},
		"zero quantity":    {{ItemID: "zinger", Quantity: 0}},
		"huge quantity":    {{ItemID: "zinger", Quantity: 51}},
		"foreign option":   {{ItemID: "cola", VariationID: "xl", Quantity: 1}},
		"option on simple": {{ItemID: "zinger", VariationID: "s", Quantity: 1}},
	}
	for name, items := range cases {
		if _, err := priceCart(testMenu(), items); !domain.IsValidationError(err) {
			t.Errorf("%s: expected validation error, got %v", name, err)
		}
	}
}
