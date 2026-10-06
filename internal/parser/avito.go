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

	params.Query = strings.TrimSpace(params.Query)
	params.City = strings.TrimSpace(params.City)

	if params.Query == "" {
		return nil, fmt.Errorf("search query is empty")
	}

	// По умолчанию берём только 5 объявлений.
	limit := params.Limit

	if limit <= 0 {
		limit = 5
	}

	if limit > 100 {
		limit = 100
	}

	if p.browserCtx == nil {
		return nil, fmt.Errorf("browser context is nil")
	}

	select {
	case <-p.browserCtx.Done():
		return nil, fmt.Errorf(
			"browser context is already closed: %w",
			p.browserCtx.Err(),
		)
	default:
	}

	searchCtx, cancel := chromedp.NewContext(p.browserCtx)
	defer cancel()

	searchCtx, timeoutCancel := context.WithTimeout(
		searchCtx,
		90*time.Second,
	)
	defer timeoutCancel()

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				timeoutCancel()
			case <-searchCtx.Done():
			}
		}()
	}

	searchURL := buildAvitoSearchURL(
		params.Query,
		params.City,
	)

	fmt.Printf(
		"🔎 Avito search: %s\n",
		searchURL,
	)

	err := chromedp.Run(
		searchCtx,
		chromedp.Navigate(searchURL),
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

	// Немного прокручиваем страницу,
	// чтобы Avito загрузил дополнительные объявления.
	for i := 0; i < 5; i++ {

		err = chromedp.Run(
			searchCtx,
			chromedp.Evaluate(
				`window.scrollTo(0, document.body.scrollHeight);`,
				nil,
			),
			chromedp.Sleep(700*time.Millisecond),
		)

		if err != nil {
			return nil, fmt.Errorf(
				"scroll Avito page: %w",
				err,
			)
		}
	}

	var rawListings []avitoRawListing

	script := `
		(() => {
			const result = [];

			const cards = document.querySelectorAll(
				'[data-marker="item"]'
			);

			for (const card of cards) {

				const titleElement =
					card.querySelector('[itemprop="name"]') ||
					card.querySelector('[data-marker="item-title"]');

				const title =
					titleElement
						? titleElement.textContent.trim()
						: '';

				const linkElement =
					card.querySelector('a[href*="/avito.ru/"]') ||
					card.querySelector('a[data-marker="item-title"]') ||
					card.querySelector('a');

				const href =
					linkElement
						? linkElement.href
						: '';

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

				let externalId = '';

				const idElement =
					card.querySelector('[data-item-id]');

				if (idElement) {
					externalId =
						idElement.getAttribute('data-item-id') || '';
				}

				if (!externalId && href) {

					const match =
						href.match(/_(\d+)(?:\?|$)/);

					if (match) {
						externalId = match[1];
					}
				}

				let city = '';

				const locationElement =
					card.querySelector(
						'[data-marker="item-address"]'
					);

				if (locationElement) {
					city =
						locationElement.textContent.trim();
				}

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

	if len(rawListings) == 0 {
		fmt.Println("⚠️ Avito: объявления не найдены")

		return []models.Listing{}, nil
	}

	// Ограничиваем количество объявлений
	// непосредственно после получения результатов поиска.
	if len(rawListings) > limit {
		rawListings = rawListings[:limit]
	}

	listings := make(
		[]models.Listing,
		0,
		len(rawListings),
	)

	for _, raw := range rawListings {

		externalID := strings.TrimSpace(
			raw.ExternalID,
		)

		if externalID == "" {
			externalID = extractAvitoID(
				raw.Href,
			)
		}

		if externalID == "" ||
			strings.TrimSpace(raw.Href) == "" {

			continue
		}

		city := strings.TrimSpace(
			raw.City,
		)

		if city == "" {
			city = extractAvitoCity(
				raw.Href,
			)
		}

		price := parsePrice(raw.Price)
		now := time.Now()

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
	}

	fmt.Printf(
		"✅ Avito: найдено %d объявлений\n",
		len(listings),
	)

	return listings, nil
}

func extractAvitoCity(
	rawURL string,
) string {

	u, err := url.Parse(rawURL)

	if err != nil {
		return ""
	}

	parts := strings.Split(
		strings.Trim(u.Path, "/"),
		"/",
	)

	if len(parts) == 0 {
		return ""
	}

	return strings.TrimSpace(
		parts[0],
	)
}

func extractAvitoID(
	rawURL string,
) string {

	if strings.TrimSpace(rawURL) == "" {
		return ""
	}

	u, err := url.Parse(rawURL)

	if err != nil {
		return ""
	}

	parts := strings.Split(
		strings.Trim(u.Path, "/"),
		"/",
	)

	if len(parts) == 0 {
		return ""
	}

	lastPart := parts[len(parts)-1]

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

func parsePrice(
	raw string,
) *int64 {

	raw = strings.TrimSpace(raw)

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

func buildAvitoSearchURL(
	query string,
	city string,
) string {

	query = strings.TrimSpace(query)
	city = strings.TrimSpace(city)

	encodedQuery := url.QueryEscape(query)

	if city != "" {

		city = strings.ToLower(city)

		return fmt.Sprintf(
			"https://www.avito.ru/%s?q=%s",
			url.PathEscape(city),
			encodedQuery,
		)
	}

	return fmt.Sprintf(
		"https://www.avito.ru/rossiya?q=%s",
		encodedQuery,
	)
}
