package models

import "time"

type SearchQuery struct {
	ID             int64
	SourceID       int
	Query          string
	City           string
	LastSearchedAt time.Time
	CreatedAt      time.Time
}
