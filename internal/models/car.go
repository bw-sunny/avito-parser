package models

type CarBrand struct {
	ID   int
	Name string
}

type CarModel struct {
	ID      int
	BrandID int
	Name    string
}

type CarGeneration struct {
	ID       int
	ModelID  int
	Name     string
	YearFrom *int
	YearTo   *int
}
