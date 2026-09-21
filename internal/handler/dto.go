package handler

import "avito-parser/internal/models"

// ListingDTO — публичное представление объявления
// для HTTP API.
//
// В DTO не передаём внутренние поля базы данных,
// которые не нужны мобильному приложению.
type ListingDTO struct {
	ExternalID  string `json:"external_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Price       *int64 `json:"price"`
	Currency    string `json:"currency"`
	URL         string `json:"url"`
	City        string `json:"city"`
	IsAvailable bool   `json:"is_available"`
	Relevance   int    `json:"relevance"`
}

// toListingDTO преобразует внутреннюю модель Listing
// в публичный DTO для API.
func toListingDTO(
	listing models.Listing,
) ListingDTO {

	return ListingDTO{
		ExternalID:  listing.ExternalID,
		Title:       listing.Title,
		Description: listing.Description,
		Price:       listing.Price,
		Currency:    listing.Currency,
		URL:         listing.URL,
		City:        listing.City,
		IsAvailable: listing.IsAvailable,
		Relevance:   listing.Relevance,
	}
}

// toListingsDTO преобразует список объявлений
// в список DTO.
func toListingsDTO(
	listings []models.Listing,
) []ListingDTO {

	result := make(
		[]ListingDTO,
		0,
		len(listings),
	)

	for _, listing := range listings {
		result = append(
			result,
			toListingDTO(listing),
		)
	}

	return result
}
