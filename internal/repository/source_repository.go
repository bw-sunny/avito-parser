package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Source struct {
	ID      int
	Code    string
	Name    string
	Enabled bool
}

type SourceRepository struct {
	db *pgxpool.Pool
}

func NewSourceRepository(db *pgxpool.Pool) *SourceRepository {
	return &SourceRepository{
		db: db,
	}
}

// GetByCode возвращает источник по его коду.
// Например:
// "avito" -> source ID 1
// "drom"  -> source ID 2
func (r *SourceRepository) GetByCode(
	ctx context.Context,
	code string,
) (*Source, error) {

	const query = `
		SELECT
			id,
			code,
			name,
			enabled
		FROM sources
		WHERE code = $1
		LIMIT 1
	`

	var source Source

	err := r.db.QueryRow(
		ctx,
		query,
		code,
	).Scan(
		&source.ID,
		&source.Code,
		&source.Name,
		&source.Enabled,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"get source by code %q: %w",
			code,
			err,
		)
	}

	return &source, nil
}
