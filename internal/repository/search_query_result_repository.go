package repository

import (
	"context"
	"fmt"

	"avito-parser/internal/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SearchQueryResultRepository struct {
	db *pgxpool.Pool
}

func NewSearchQueryResultRepository(
	db *pgxpool.Pool,
) *SearchQueryResultRepository {
	return &SearchQueryResultRepository{
		db: db,
	}
}

// Add связывает поисковый запрос с объявлением.
func (r *SearchQueryResultRepository) Add(
	ctx context.Context,
	result *models.SearchQueryResult,
) error {

	const query = `
		INSERT INTO search_query_results (
			search_query_id,
			listing_id,
			position
		)
		VALUES ($1, $2, $3)
		ON CONFLICT (search_query_id, listing_id)
		DO UPDATE SET
			position = EXCLUDED.position
	`

	_, err := r.db.Exec(
		ctx,
		query,
		result.SearchQueryID,
		result.ListingID,
		result.Position,
	)

	if err != nil {
		return fmt.Errorf(
			"add search query result: %w",
			err,
		)
	}

	return nil
}

// Clear удаляет старые результаты конкретного поиска.
func (r *SearchQueryResultRepository) Clear(
	ctx context.Context,
	searchQueryID int64,
) error {

	const query = `
		DELETE FROM search_query_results
		WHERE search_query_id = $1
	`

	_, err := r.db.Exec(
		ctx,
		query,
		searchQueryID,
	)

	if err != nil {
		return fmt.Errorf(
			"clear search query results: %w",
			err,
		)
	}

	return nil
}

// GetListingIDs возвращает ID объявлений,
// относящихся к поисковому запросу,
// в порядке выдачи.
func (r *SearchQueryResultRepository) GetListingIDs(
	ctx context.Context,
	searchQueryID int64,
) ([]int64, error) {

	const query = `
		SELECT listing_id
		FROM search_query_results
		WHERE search_query_id = $1
		ORDER BY position ASC
	`

	rows, err := r.db.Query(
		ctx,
		query,
		searchQueryID,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"get search query results: %w",
			err,
		)
	}

	defer rows.Close()

	var listingIDs []int64

	for rows.Next() {

		var listingID int64

		if err := rows.Scan(&listingID); err != nil {
			return nil, fmt.Errorf(
				"scan search query result: %w",
				err,
			)
		}

		listingIDs = append(
			listingIDs,
			listingID,
		)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate search query results: %w",
			err,
		)
	}

	return listingIDs, nil
}
