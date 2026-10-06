package service

import (
	"testing"

	"avito-parser/internal/models"
)

func rel(title string, q Query) int {
	return CalculateRelevanceFor(models.Listing{Title: title}, q)
}

func TestRanking(t *testing.T) {
	q := Query{Part: "колодки передние", Car: "BMW 3 Series E90"}

	cases := []struct {
		title string
		min   int
		max   int
	}{
		{"Тормозные колодки передние BMW E90 новые", 90, 100},
		{"Колодки BMW E90/E91", 70, 100},
		{"Колодки передние Brembo BMW 3 E90", 85, 100},
		{"Колодки задние BMW E90", 50, 85},
		{"Колодки передние BMW F30", 40, 85},
		{"Колодки передние Audi A4", 0, 15},
		{"Тормозные диски BMW E90", 0, 15},
		{"BMW E90 2008 г. седан", 0, 30},
		{"Куплю колодки BMW E90", 0, 60},
		{"Колодки универсальные", 20, 60},
	}

	for _, c := range cases {
		got := rel(c.title, q)
		t.Logf("%3d  %s", got, c.title)
		if got < c.min || got > c.max {
			t.Errorf("%q: got %d, want %d..%d", c.title, got, c.min, c.max)
		}
	}
}

func TestMorphology(t *testing.T) {
	q := Query{Part: "колодки", Car: "Toyota Camry XV50"}
	a := rel("Колодок тормозных Тойота Camry XV50", q)
	b := rel("Колодки Toyota Camry XV50", q)
	t.Logf("%d %d", a, b)
	if a < 80 || b < 90 {
		t.Errorf("morphology: %d %d", a, b)
	}
}

func TestSpecTokensNotGeneration(t *testing.T) {
	q := Query{Part: "диски литые r16", Car: "Kia Rio"}
	got := rel("Литые диски R17 Kia Rio", q)
	t.Logf("%d", got)
	if got < 40 {
		t.Errorf("r17 treated as body code? %d", got)
	}
}

func TestUsedNew(t *testing.T) {
	q := Query{Part: "фара новая", Car: "Lada Granta"}
	n := rel("Фара новая Lada Granta", q)
	u := rel("Фара б/у Lada Granta", q)
	t.Logf("%d %d", n, u)
	if n <= u {
		t.Errorf("new %d <= used %d", n, u)
	}
}

func TestLegacyStringSplit(t *testing.T) {
	sp := splitRawQuery("колодки BMW E90 brembo")
	t.Logf("%+v", sp)
	if sp.Car != "bmw e90" || sp.Part != "колодки brembo" {
		t.Errorf("split: %+v", sp)
	}
	got := CalculateRelevance(models.Listing{Title: "Колодки Brembo BMW E90"}, "колодки BMW E90 brembo")
	if got < 90 {
		t.Errorf("legacy: %d", got)
	}
}

func TestSortStable(t *testing.T) {
	ls := []models.Listing{
		{Title: "Диски BMW E90"},
		{Title: "Колодки BMW E90"},
		{Title: "Колодки передние BMW E90"},
	}
	SortByRelevanceFor(ls, Query{Part: "колодки передние", Car: "BMW E90"})
	if ls[0].Title != "Колодки передние BMW E90" || ls[2].Title != "Диски BMW E90" {
		t.Errorf("order: %+v", ls)
	}
}

func TestPartBrandMatters(t *testing.T) {
	q := Query{Part: "колодки brembo", Car: "BMW E90"}

	brembo := rel("Колодки тормозные задние Brembo P06033 E90", q)
	bosch := rel("Тормозные колодки перед bosch 0986494118 BMW E90", q)
	none := rel("Тормозные колодки задние BMW E81,E82,E87,E88,E90", q)

	t.Logf("brembo=%d bosch=%d none=%d", brembo, bosch, none)

	if brembo <= bosch {
		t.Errorf("brembo match (%d) should outrank bosch conflict (%d)", brembo, bosch)
	}
	if brembo <= none {
		t.Errorf("brembo match (%d) should outrank no-brand listing (%d)", brembo, none)
	}
}

func TestNoFalsePositiveOnGenericAdjective(t *testing.T) {
	q := Query{Part: "тормозные колодки", Car: "BMW E90"}

	brakePads := rel("Тормозные колодки передние BMW E90", q)
	handbrake := rel("Колодки стояночного тормоза BMW E39 E46 E90 F30", q)

	t.Logf("brakePads=%d handbrake=%d", brakePads, handbrake)

	if handbrake >= brakePads {
		t.Errorf("handbrake pads (%d) should not outrank real match (%d)", handbrake, brakePads)
	}
}
