package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"avito-parser/internal/parser"
	"avito-parser/internal/service"
)

// SearchHandler отвечает за HTTP-запросы поиска.
type SearchHandler struct {
	searchService *service.SearchService
}

// NewSearchHandler создаёт новый SearchHandler.
func NewSearchHandler(
	searchService *service.SearchService,
) *SearchHandler {
	return &SearchHandler{
		searchService: searchService,
	}
}

// Search обрабатывает GET /api/search.
func (h *SearchHandler) Search(
	w http.ResponseWriter,
	r *http.Request,
) {

	// =========================
	// METHOD
	// =========================

	if r.Method != http.MethodGet {
		writeJSON(
			w,
			http.StatusMethodNotAllowed,
			map[string]interface{}{
				"error": "method not allowed",
			},
		)

		return
	}

	// =========================
	// QUERY
	// =========================

	query := strings.TrimSpace(
		r.URL.Query().Get("query"),
	)

	if query == "" {
		writeJSON(
			w,
			http.StatusBadRequest,
			map[string]interface{}{
				"error": "query parameter is required",
			},
		)

		return
	}

	// =========================
	// SOURCE
	// =========================

	source := strings.TrimSpace(
		r.URL.Query().Get("source"),
	)

	// По умолчанию ищем на Avito.
	if source == "" {
		source = "avito"
	}

	// =========================
	// CITY
	// =========================

	city := strings.TrimSpace(
		r.URL.Query().Get("city"),
	)

	// =========================
	// LIMIT
	// =========================

	limit := 25

	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {

		parsedLimit, err := strconv.Atoi(rawLimit)

		if err != nil || parsedLimit <= 0 {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]interface{}{
					"error": "limit must be a positive integer",
				},
			)

			return
		}

		limit = parsedLimit
	}

	// Максимум объявлений за один запрос.
	if limit > 100 {
		limit = 100
	}

	// =========================
	// SEARCH
	// =========================

	listings, err := h.searchService.Search(
		r.Context(),
		source,
		parser.SearchParams{
			Query: query,
			City:  city,
			Limit: limit,
		},
	)

	if err != nil {
		writeJSON(
			w,
			http.StatusInternalServerError,
			map[string]interface{}{
				"error": err.Error(),
			},
		)

		return
	}

	// =========================
	// DTO
	// =========================

	items := toListingsDTO(listings)

	// =========================
	// RESPONSE
	// =========================

	response := SearchResponse{
		Query:  query,
		Source: source,
		Count:  len(items),
		Items:  items,
	}

	writeJSON(
		w,
		http.StatusOK,
		response,
	)
}

// SearchResponse — ответ API.
type SearchResponse struct {
	Query  string       `json:"query"`
	Source string       `json:"source"`
	Count  int          `json:"count"`
	Items  []ListingDTO `json:"items"`
}

// writeJSON отправляет JSON-ответ клиенту.
func writeJSON(
	w http.ResponseWriter,
	status int,
	data interface{},
) {
	w.Header().Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
	}
}
