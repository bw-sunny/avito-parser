package models

import "time"

type SearchQueryResult struct {
	SearchQueryID int64
	ListingID     int64
	Position      int
	CreatedAt     time.Time
}
