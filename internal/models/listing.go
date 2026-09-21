package models

import "time"

type Listing struct {
	ID          int64
	SourceID    int
	ExternalID  string
	Title       string
	Description string
	Price       *int64
	Currency    string
	URL         string
	City        string
	Region      string
	SellerType  string
	Condition   string
	IsAvailable bool
	PublishedAt *time.Time
	ParsedAt    time.Time
	UpdatedAt   time.Time
	Relevance   int `json:"relevance"`
}
