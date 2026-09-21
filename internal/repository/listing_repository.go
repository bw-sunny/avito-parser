package repository

import (
	"context"
	"fmt"

	"avito-parser/internal/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ListingRepository struct {
	db *pgxpool.Pool
}

func NewListingRepository(db *pgxpool.Pool) *ListingRepository {
	return &ListingRepository{
		db: db,
	}
}

func (r *ListingRepository) Create(
	ctx context.Context,
	listing *models.Listing,
) error {

	query := `
		INSERT INTO part_listings (
			source_id,
			external_id,
			title,
			description,
			price,
			currency,
			url,
			city,
			region,
			seller_type,
			condition,
			is_available,
			published_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13
		)
		ON CONFLICT (source_id, external_id)
		DO UPDATE SET
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			price = EXCLUDED.price,
			currency = EXCLUDED.currency,
			url = EXCLUDED.url,
			city = EXCLUDED.city,
			region = EXCLUDED.region,
			seller_type = EXCLUDED.seller_type,
			condition = EXCLUDED.condition,
			is_available = EXCLUDED.is_available,
			published_at = EXCLUDED.published_at,
			updated_at = NOW()
	`

	_, err := r.db.Exec(
		ctx,
		query,
		listing.SourceID,
		listing.ExternalID,
		listing.Title,
		listing.Description,
		listing.Price,
		listing.Currency,
		listing.URL,
		listing.City,
		listing.Region,
		listing.SellerType,
		listing.Condition,
		listing.IsAvailable,
		listing.PublishedAt,
	)

	if err != nil {
		return fmt.Errorf("ошибка сохранения объявления: %w", err)
	}

	return nil
}

// GetByIDs возвращает объявления по списку ID.
// Порядок результатов соответствует порядку,
// указанному в PostgreSQL через array_position.
func (r *ListingRepository) GetByIDs(
	ctx context.Context,
	ids []int64,
) ([]models.Listing, error) {

	if len(ids) == 0 {
		return []models.Listing{}, nil
	}

	const query = `
		SELECT
			id,
			source_id,
			external_id,
			title,
			description,
			price,
			currency,
			url,
			city,
			region,
			seller_type,
			condition,
			is_available,
			published_at,
			parsed_at,
			updated_at
		FROM part_listings
		WHERE id = ANY($1)
		ORDER BY array_position($1, id)
	`

	rows, err := r.db.Query(
		ctx,
		query,
		ids,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"get listings by ids: %w",
			err,
		)
	}

	defer rows.Close()

	listings := make([]models.Listing, 0, len(ids))

	for rows.Next() {

		var listing models.Listing

		err := rows.Scan(
			&listing.ID,
			&listing.SourceID,
			&listing.ExternalID,
			&listing.Title,
			&listing.Description,
			&listing.Price,
			&listing.Currency,
			&listing.URL,
			&listing.City,
			&listing.Region,
			&listing.SellerType,
			&listing.Condition,
			&listing.IsAvailable,
			&listing.PublishedAt,
			&listing.ParsedAt,
			&listing.UpdatedAt,
		)

		if err != nil {
			return nil, fmt.Errorf(
				"scan listing: %w",
				err,
			)
		}

		listings = append(
			listings,
			listing,
		)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate listings: %w",
			err,
		)
	}

	return listings, nil
}

// FindID возвращает внутренний ID объявления
// по источнику и внешнему ID площадки.
func (r *ListingRepository) FindID(
	ctx context.Context,
	sourceID int,
	externalID string,
) (int64, error) {

	const query = `
		SELECT id
		FROM part_listings
		WHERE source_id = $1
		  AND external_id = $2
		LIMIT 1
	`

	var id int64

	err := r.db.QueryRow(
		ctx,
		query,
		sourceID,
		externalID,
	).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf(
			"find listing %s: %w",
			externalID,
			err,
		)
	}

	return id, nil
}
