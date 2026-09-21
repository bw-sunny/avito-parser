package models

type PartCategory struct {
	ID       int
	ParentID *int
	Name     string
	Slug     string
}
