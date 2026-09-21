package parser

import (
	"context"

	"avito-parser/internal/models"
)

type SearchParams struct {
	Query string

	City string

	Limit int
}

type Parser interface {
	Name() string

	Search(
		ctx context.Context,
		params SearchParams,
	) ([]models.Listing, error)
}
