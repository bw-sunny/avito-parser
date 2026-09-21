package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"avito-parser/internal/models"
	"avito-parser/internal/parser"
	"avito-parser/internal/repository"
)

const searchCacheTTL = 60 * time.Minute

type SearchService struct {
	parsers map[string]parser.Parser

	listingRepository           *repository.ListingRepository
	sourceRepository            *repository.SourceRepository
	searchQueryRepository       *repository.SearchQueryRepository
	searchQueryResultRepository *repository.SearchQueryResultRepository
}

func NewSearchService(
	listingRepository *repository.ListingRepository,
	sourceRepository *repository.SourceRepository,
	searchQueryRepository *repository.SearchQueryRepository,
	searchQueryResultRepository *repository.SearchQueryResultRepository,
	parsers ...parser.Parser,
) *SearchService {

	parserMap := make(map[string]parser.Parser)

	for _, p := range parsers {
		if p == nil {
			continue
		}

		parserMap[p.Name()] = p
	}

	return &SearchService{
		parsers:                     parserMap,
		listingRepository:           listingRepository,
		sourceRepository:            sourceRepository,
		searchQueryRepository:       searchQueryRepository,
		searchQueryResultRepository: searchQueryResultRepository,
	}
}

func (s *SearchService) Search(
	ctx context.Context,
	source string,
	params parser.SearchParams,
) ([]models.Listing, error) {

	// =========================================================
	// VALIDATION
	// =========================================================

	source = strings.ToLower(
		strings.TrimSpace(source),
	)

	if source == "" {
		return nil, fmt.Errorf(
			"search source is empty",
		)
	}

	p, ok := s.parsers[source]

	if !ok {
		return nil, fmt.Errorf(
			"parser for source %q not found",
			source,
		)
	}

	params.Query = strings.TrimSpace(
		params.Query,
	)

	params.City = strings.TrimSpace(
		params.City,
	)

	if params.Query == "" {
		return nil, fmt.Errorf(
			"search query is empty",
		)
	}

	if params.Limit <= 0 {
		params.Limit = 25
	}

	if params.Limit > 100 {
		params.Limit = 100
	}

	// =========================================================
	// SOURCE
	// =========================================================

	sourceID, err := s.getSourceID(
		ctx,
		source,
	)

	if err != nil {
		return nil, err
	}

	// =========================================================
	// CACHE
	// =========================================================

	if s.searchQueryRepository != nil &&
		s.searchQueryResultRepository != nil &&
		s.listingRepository != nil {

		cachedListings, found, err := s.getCachedResults(
			ctx,
			sourceID,
			params,
		)

		if err != nil {
			return nil, err
		}

		if found {
			fmt.Printf(
				"⚡ Cache hit: %s\n",
				params.Query,
			)

			return cachedListings, nil
		}
	}

	// =========================================================
	// PARSER
	// =========================================================

	fmt.Printf(
		"🌐 Cache miss: searching %s\n",
		source,
	)

	listings, err := p.Search(
		ctx,
		params,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"search %s: %w",
			source,
			err,
		)
	}

	// =========================================================
	// RELEVANCE
	// =========================================================

	// Рассчитываем и сохраняем рейтинг релевантности
	// для каждого объявления.
	for i := range listings {

		listings[i].Relevance = CalculateRelevance(
			listings[i],
			params.Query,
		)
	}

	// Сортируем по рассчитанной релевантности.
	SortByRelevance(
		listings,
		params.Query,
	)

	// После сортировки ещё раз гарантируем,
	// что поле Relevance соответствует текущему
	// порядку и значению.
	for i := range listings {

		listings[i].Relevance = CalculateRelevance(
			listings[i],
			params.Query,
		)
	}

	// =========================================================
	// LIMIT
	// =========================================================

	// Ограничиваем количество результатов
	// только после сортировки.
	if len(listings) > params.Limit {
		listings = listings[:params.Limit]
	}

	// =========================================================
	// SAVE LISTINGS
	// =========================================================

	if s.listingRepository != nil {

		for i := range listings {

			err := s.listingRepository.Create(
				ctx,
				&listings[i],
			)

			if err != nil {
				return nil, fmt.Errorf(
					"save listing %s: %w",
					listings[i].ExternalID,
					err,
				)
			}
		}
	}

	// =========================================================
	// SAVE SEARCH QUERY + RESULTS
	// =========================================================

	if s.searchQueryRepository != nil &&
		s.searchQueryResultRepository != nil &&
		s.listingRepository != nil {

		err := s.saveSearchResults(
			ctx,
			sourceID,
			params,
			listings,
		)

		if err != nil {
			return nil, err
		}
	}

	return listings, nil
}

// =============================================================
// SOURCE
// =============================================================

func (s *SearchService) getSourceID(
	ctx context.Context,
	source string,
) (int, error) {

	if s.sourceRepository == nil {
		return 0, fmt.Errorf(
			"source repository is not configured",
		)
	}

	result, err := s.sourceRepository.GetByCode(
		ctx,
		source,
	)

	if err != nil {
		return 0, fmt.Errorf(
			"get source %q: %w",
			source,
			err,
		)
	}

	if !result.Enabled {
		return 0, fmt.Errorf(
			"source %q is disabled",
			source,
		)
	}

	return result.ID, nil
}

// =============================================================
// GET CACHED RESULTS
// =============================================================

func (s *SearchService) getCachedResults(
	ctx context.Context,
	sourceID int,
	params parser.SearchParams,
) ([]models.Listing, bool, error) {

	searchQuery, err := s.searchQueryRepository.Get(
		ctx,
		sourceID,
		params.Query,
		params.City,
	)

	if err != nil {
		return nil, false, fmt.Errorf(
			"get search query: %w",
			err,
		)
	}

	// Поискового запроса ещё не было.
	if searchQuery == nil {
		return nil, false, nil
	}

	// =========================================================
	// CHECK TTL
	// =========================================================

	if time.Since(searchQuery.LastSearchedAt) >= searchCacheTTL {

		fmt.Printf(
			"♻️ Cache expired: %s\n",
			params.Query,
		)

		return nil, false, nil
	}

	// =========================================================
	// GET LISTING IDS
	// =========================================================

	listingIDs, err := s.searchQueryResultRepository.GetListingIDs(
		ctx,
		searchQuery.ID,
	)

	if err != nil {
		return nil, false, fmt.Errorf(
			"get cached listing IDs: %w",
			err,
		)
	}

	if len(listingIDs) == 0 {
		return nil, false, nil
	}

	// =========================================================
	// GET LISTINGS
	// =========================================================

	listings, err := s.listingRepository.GetByIDs(
		ctx,
		listingIDs,
	)

	if err != nil {
		return nil, false, fmt.Errorf(
			"get cached listings: %w",
			err,
		)
	}

	// =========================================================
	// RELEVANCE FOR CACHE
	// =========================================================

	// Relevance не хранится в БД как отдельное поле,
	// поэтому при cache hit рассчитываем его заново.
	for i := range listings {

		listings[i].Relevance = CalculateRelevance(
			listings[i],
			params.Query,
		)
	}

	// Сортируем кэшированные объявления.
	SortByRelevance(
		listings,
		params.Query,
	)

	// =========================================================
	// LIMIT
	// =========================================================

	if len(listings) > params.Limit {
		listings = listings[:params.Limit]
	}

	return listings, true, nil
}

// =============================================================
// SAVE SEARCH RESULTS
// =============================================================

func (s *SearchService) saveSearchResults(
	ctx context.Context,
	sourceID int,
	params parser.SearchParams,
	listings []models.Listing,
) error {

	now := time.Now()

	searchQuery := &models.SearchQuery{
		SourceID:       sourceID,
		Query:          params.Query,
		City:           params.City,
		LastSearchedAt: now,
	}

	err := s.searchQueryRepository.Create(
		ctx,
		searchQuery,
	)

	if err != nil {
		return fmt.Errorf(
			"save search query: %w",
			err,
		)
	}

	// =========================================================
	// GET SAVED SEARCH QUERY
	// =========================================================

	searchQuery, err = s.searchQueryRepository.Get(
		ctx,
		sourceID,
		params.Query,
		params.City,
	)

	if err != nil {
		return fmt.Errorf(
			"get saved search query: %w",
			err,
		)
	}

	if searchQuery == nil {
		return fmt.Errorf(
			"saved search query not found",
		)
	}

	// =========================================================
	// CLEAR OLD RESULTS
	// =========================================================

	err = s.searchQueryResultRepository.Clear(
		ctx,
		searchQuery.ID,
	)

	if err != nil {
		return fmt.Errorf(
			"clear old search results: %w",
			err,
		)
	}

	// =========================================================
	// SAVE NEW RESULTS
	// =========================================================

	for i := range listings {

		listingID, err := s.listingRepository.FindID(
			ctx,
			listings[i].SourceID,
			listings[i].ExternalID,
		)

		if err != nil {
			return fmt.Errorf(
				"find listing %s: %w",
				listings[i].ExternalID,
				err,
			)
		}

		result := &models.SearchQueryResult{
			SearchQueryID: searchQuery.ID,
			ListingID:     listingID,
			Position:      i,
		}

		err = s.searchQueryResultRepository.Add(
			ctx,
			result,
		)

		if err != nil {
			return fmt.Errorf(
				"save search result: %w",
				err,
			)
		}
	}

	return nil
}
