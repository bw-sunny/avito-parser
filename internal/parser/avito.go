package parser

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"avito-parser/internal/models"

	"github.com/chromedp/chromedp"
)

type AvitoParser struct {
	browserCtx context.Context
}

type avitoRawListing struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	Href       string `json:"href"`
	Price      string `json:"price"`
	Image      string `json:"image"`
	City       string `json:"city"`
}

func NewAvitoParser(ctx context.Context) *AvitoParser {
	return &AvitoParser{
		browserCtx: ctx,
	}
}

func (p *AvitoParser) Name() string {
	return "avito"
}

func (p *AvitoParser) Search(
	ctx context.Context,
	params SearchParams,
) ([]models.Listing, error) {

	// =========================================================
	// VALIDATION
	// =========================================================

	params.Query = strings.TrimSpace(params.Query)
	params.City = strings.TrimSpace(params.City)

	if params.Query == "" {
		return nil, fmt.Errorf("search query is empty")
	}

	limit := params.Limit

	if limit <= 0 {
		limit = 25
	}

	if limit > 100 {
		limit = 100
	}

	// =========================================================
	// BROWSER CONTEXT
	// =========================================================

	if p.browserCtx == nil {
		return nil, fmt.Errorf("browser context is nil")
	}

	// Проверяем, что allocator/browser context
	// ещё не был отменён.
	select {
	case <-p.browserCtx.Done():
		return nil, fmt.Errorf(
			"browser context is already closed: %w",
			p.browserCtx.Err(),
		)
	default:
	}

	// Создаём отдельный context для каждого поиска.
	//
	// Это важно:
	// один запрос не должен использовать один и тот же
	// tab/context с другим запросом.
	searchCtx, cancel := chromedp.NewContext(
		p.browserCtx,
	)

	defer cancel()

	// =========================================================
	// SEARCH TIMEOUT
	// =========================================================

	searchCtx, timeoutCancel := context.WithTimeout(
		searchCtx,
		90*time.Second,
	)

	defer timeoutCancel()

	// Если HTTP-запрос клиента отменился,
	// закрываем только текущий поиск.
	if ctx != nil {

		go func() {

			select {

			case <-ctx.Done():
				timeoutCancel()

			case <-searchCtx.Done():
			}

		}()
	}

	// =========================================================
	// SEARCH URL
	// =========================================================

	searchURL := buildAvitoSearchURL(
		params.Query,
		params.City,
	)

	fmt.Printf(
		"🔎 Avito search: %s\n",
		searchURL,
	)

	// =========================================================
	// OPEN AVITO
	// =========================================================

	err := chromedp.Run(
		searchCtx,

		chromedp.Navigate(searchURL),

		// Avito использует динамический JavaScript-контент.
		// Поэтому после Navigate ждём загрузку DOM.
		chromedp.Sleep(8*time.Second),

		chromedp.WaitVisible(
			`[data-marker="catalog-serp"]`,
			chromedp.ByQuery,
		),
	)

	if err != nil {
		return nil, fmt.Errorf(
			"open Avito search page: %w",
			err,
		)
	}

	// =========================================================
	// SCROLL
	// =========================================================

	for i := 0; i < 5; i++ {

		err = chromedp.Run(
			searchCtx,

			chromedp.Evaluate(
				`window.scrollTo(0, document.body.scrollHeight);`,
				nil,
			),

			chromedp.Sleep(
				700*time.Millisecond,
			),
		)

		if err != nil {
			return nil, fmt.Errorf(
				"scroll Avito page: %w",
				err,
			)
		}
	}

	// =========================================================
	// EXTRACT DOM
	// =========================================================

	var rawListings []avitoRawListing

	script := `
	(() => {

		const result = [];

		const cards = document.querySelectorAll(
			'[data-marker="item"]'
		);

		for (const card of cards) {

			// =================================================
			// TITLE
			// =================================================

			const titleElement =
				card.querySelector('[itemprop="name"]') ||
				card.querySelector('[data-marker="item-title"]');

			const title =
				titleElement
					? titleElement.textContent.trim()
					: '';

			// =================================================
			// LINK
			// =================================================

			const linkElement =
				card.querySelector('a[href*="/avito.ru/"]') ||
				card.querySelector('a[data-marker="item-title"]') ||
				card.querySelector('a');

			const href =
				linkElement
					? linkElement.href
					: '';

			// =================================================
			// PRICE
			// =================================================

			const priceElement =
				card.querySelector('[itemprop="price"]') ||
				card.querySelector('[data-marker="item-price"]');

			const price =
				priceElement
					? (
						priceElement.getAttribute('content') ||
						priceElement.textContent.trim()
					)
					: '';

			// =================================================
			// IMAGE
			// =================================================

			const imageElement =
				card.querySelector('img');

			const image =
				imageElement
					? (
						imageElement.src ||
						imageElement.getAttribute('src') ||
						''
					)
					: '';

			// =================================================
			// EXTERNAL ID
			// =================================================

			let externalId = '';

			const idElement =
				card.querySelector('[data-item-id]');

			if (idElement) {

				externalId =
					idElement.getAttribute('data-item-id') || '';
			}

			// Если data-item-id отсутствует,
			// пробуем получить ID из URL.
			if (!externalId && href) {

				const match =
					href.match(/_(\d+)(?:\?|$)/);

				if (match) {
					externalId = match[1];
				}
			}

			// =================================================
			// CITY
			// =================================================

			let city = '';

			const locationElement =
				card.querySelector(
					'[data-marker="item-address"]'
				);

			if (locationElement) {

				city =
					locationElement.textContent.trim();
			}

			// =================================================
			// RESULT
			// =================================================

			if (title || href) {

				result.push({
					externalId,
					title,
					href,
					price,
					image,
					city
				});
			}
		}

		return result;

	})()
	`

	err = chromedp.Run(
		searchCtx,

		chromedp.Evaluate(
			script,
			&rawListings,
		),
	)

	if err != nil {
		return nil, fmt.Errorf(
			"extract Avito listings: %w",
			err,
		)
	}

	// =========================================================
	// EMPTY RESULT
	// =========================================================

	if len(rawListings) == 0 {
		return []models.Listing{}, nil
	}

	// =========================================================
	// CONVERT TO MODELS.LISTING
	// =========================================================

	listings := make(
		[]models.Listing,
		0,
		len(rawListings),
	)

	for _, raw := range rawListings {

		// =====================================================
		// EXTERNAL ID
		// =====================================================

		externalID := strings.TrimSpace(
			raw.ExternalID,
		)

		if externalID == "" {

			externalID = extractAvitoID(
				raw.Href,
			)
		}

		// Объявление без ID и URL нам не подходит.
		if externalID == "" ||
			strings.TrimSpace(raw.Href) == "" {

			continue
		}

		// =====================================================
		// CITY
		// =====================================================

		city := strings.TrimSpace(
			raw.City,
		)

		if city == "" {

			city = extractAvitoCity(
				raw.Href,
			)
		}

		// =====================================================
		// PRICE
		// =====================================================

		price := parsePrice(
			raw.Price,
		)

		// =====================================================
		// TIME
		// =====================================================

		now := time.Now()

		// =====================================================
		// LISTING
		// =====================================================

		listing := models.Listing{
			SourceID:    1,
			ExternalID:  externalID,
			Title:       strings.TrimSpace(raw.Title),
			Description: "",
			Price:       price,
			Currency:    "RUB",
			URL:         strings.TrimSpace(raw.Href),
			City:        city,
			Region:      "",
			SellerType:  "",
			Condition:   "",
			IsAvailable: true,
			ParsedAt:    now,
			UpdatedAt:   now,
		}

		listings = append(
			listings,
			listing,
		)

		// Ограничиваем количество результатов.
		if len(listings) >= limit {
			break
		}
	}

	fmt.Printf(
		"✅ Avito: найдено %d объявлений\n",
		len(listings),
	)

	return listings, nil
}

// =============================================================
// EXTRACT CITY
// =============================================================

func extractAvitoCity(
	rawURL string,
) string {

	u, err := url.Parse(
		rawURL,
	)

	if err != nil {
		return ""
	}

	parts := strings.Split(
		strings.Trim(
			u.Path,
			"/",
		),
		"/",
	)

	if len(parts) == 0 {
		return ""
	}

	return strings.TrimSpace(
		parts[0],
	)
}

// =============================================================
// EXTRACT ID
// =============================================================

func extractAvitoID(
	rawURL string,
) string {

	if strings.TrimSpace(rawURL) == "" {
		return ""
	}

	u, err := url.Parse(
		rawURL,
	)

	if err != nil {
		return ""
	}

	parts := strings.Split(
		strings.Trim(
			u.Path,
			"/",
		),
		"/",
	)

	if len(parts) == 0 {
		return ""
	}

	lastPart := parts[len(parts)-1]

	// ID обычно находится после последнего "_".
	//
	// Например:
	//
	// kolodki-bmw-e90_8231402426
	//
	// -> 8231402426

	for i := len(lastPart) - 1; i >= 0; i-- {

		if lastPart[i] == '_' {

			id := lastPart[i+1:]

			if _, err := strconv.ParseInt(
				id,
				10,
				64,
			); err == nil {

				return id
			}

			break
		}
	}

	return ""
}

// =============================================================
// PARSE PRICE
// =============================================================

func parsePrice(
	raw string,
) *int64 {

	raw = strings.TrimSpace(
		raw,
	)

	if raw == "" {
		return nil
	}

	var digits strings.Builder

	for _, r := range raw {

		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}

	if digits.Len() == 0 {
		return nil
	}

	value, err := strconv.ParseInt(
		digits.String(),
		10,
		64,
	)

	if err != nil {
		return nil
	}

	return &value
}

// =============================================================
// BUILD AVITO SEARCH URL
// =============================================================

func buildAvitoSearchURL(
	query string,
	city string,
) string {

	query = strings.TrimSpace(
		query,
	)

	city = strings.TrimSpace(
		city,
	)

	encodedQuery := url.QueryEscape(
		query,
	)

	// Если пользователь передал город,
	// ищем непосредственно в этом регионе.
	if city != "" {

		city = strings.ToLower(
			city,
		)

		return fmt.Sprintf(
			"https://www.avito.ru/%s?q=%s",
			url.PathEscape(city),
			encodedQuery,
		)
	}

	// Если город не указан,
	// ищем по всей России.
	return fmt.Sprintf(
		"https://www.avito.ru/rossiya?q=%s",
		encodedQuery,
	)
}
