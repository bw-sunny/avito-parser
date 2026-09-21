package repository

import (
	"context"
	"errors"
	"fmt"

	"avito-parser/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SearchQueryRepository struct {
	db *pgxpool.Pool
}

func NewSearchQueryRepository(db *pgxpool.Pool) *SearchQueryRepository {
	return &SearchQueryRepository{
		db: db,
	}
}

// Create создаёт поисковый запрос.
// Если такой запрос уже существует — обновляет время последнего поиска.
func (r *SearchQueryRepository) Create(
	ctx context.Context,
	searchQuery *models.SearchQuery,
) error {

	query := `
		INSERT INTO search_queries (
			source_id,
			query,
			city,
			last_searched_at
		)
		VALUES (
			$1, $2, $3, $4
		)
		ON CONFLICT (source_id, query, city)
		DO UPDATE SET
			last_searched_at = EXCLUDED.last_searched_at
		RETURNING id
	`

	err := r.db.QueryRow(
		ctx,
		query,
		searchQuery.SourceID,
		searchQuery.Query,
		searchQuery.City,
		searchQuery.LastSearchedAt,
	).Scan(
		&searchQuery.ID,
	)

	if err != nil {
		return fmt.Errorf(
			"create search query: %w",
			err,
		)
	}

	return nil
}

// Get получает поисковый запрос по источнику, запросу и городу.
func (r *SearchQueryRepository) Get(
	ctx context.Context,
	sourceID int,
	query string,
	city string,
) (*models.SearchQuery, error) {

	const sql = `
		SELECT
			id,
			source_id,
			query,
			city,
			last_searched_at,
			created_at
		FROM search_queries
		WHERE source_id = $1
		  AND query = $2
		  AND city = $3
		LIMIT 1
	`

	var result models.SearchQuery

	err := r.db.QueryRow(
		ctx,
		sql,
		sourceID,
		query,
		city,
	).Scan(
		&result.ID,
		&result.SourceID,
		&result.Query,
		&result.City,
		&result.LastSearchedAt,
		&result.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf(
			"get search query: %w",
			err,
		)
	}

	return &result, nil
}
