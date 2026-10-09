package parser

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"avito-parser/internal/browser"
	"avito-parser/internal/models"

	"github.com/chromedp/chromedp"
)

type AvitoParser struct {
	browser *browser.Manager
}

type avitoRawListing struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	Href       string `json:"href"`
	Price      string `json:"price"`
	Image      string `json:"image"`
	City       string `json:"city"`
}

func NewAvitoParser(b *browser.Manager) *AvitoParser {
	return &AvitoParser{
		browser: b,
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

	if p.browser == nil {
		return nil, fmt.Errorf("browser manager is nil")
	}

	searchURL := buildAvitoSearchURL(
		params.Query,
		params.City,
	)

	fmt.Printf(
		"🔎 Avito search: %s\n",
		searchURL,
	)

	var rawListings []avitoRawListing

	err := p.browser.Tab(ctx, SearchTabTimeout, func(searchCtx context.Context) error {

		err := chromedp.Run(
			searchCtx,
			chromedp.Navigate(searchURL),
			chromedp.Sleep(8*time.Second),
		)

		if err != nil {
			return fmt.Errorf("open Avito search page: %w", err)
		}

		// Капчу проверяем до ожидания целевого селектора: на
		// капче его никогда не будет, и без этой проверки
		// WaitVisible просто откатится по таймауту с невнятной
		// ошибкой вместо чёткого browser.ErrCaptcha.
		isCaptcha, err := browser.DetectCaptcha(searchCtx)

		if err != nil {
			return fmt.Errorf("detect captcha: %w", err)
		}

		if isCaptcha {

			if err := waitOrFailCaptcha(p.browser, searchCtx, "Avito"); err != nil {
				return err
			}
		}

		err = chromedp.Run(
			searchCtx,
			chromedp.WaitVisible(
				`[data-marker="catalog-serp"]`,
				chromedp.ByQuery,
			),
		)

		if err != nil {
			return fmt.Errorf("wait Avito search results: %w", err)
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
				return fmt.Errorf("scroll Avito page: %w", err)
			}
		}

		return chromedp.Run(
			searchCtx,
			chromedp.Evaluate(avitoExtractScript, &rawListings),
		)
	})

	if err != nil {
		return nil, fmt.Errorf("avito: %w", err)
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
			// SourceID проставляет SearchService после парсинга —
			// см. search_service.go. Парсер не обязан знать ID
			// источника в БД, только свой код через Name().
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

// avitoExtractScript — старое тело script внутри Search() вынесено
// в константу, т.к. само извлечение теперь вызывается из замыкания.
const avitoExtractScript = `
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

// kopecksSuffixPattern вырезает копейки перед тем, как парсить
// цену как целые рубли.
//
// БАГ, который это чинит: сырая цена с Drom вида "9 002,40 ₽"
// (9002 рубля 40 копеек) раньше превращалась в 900240, потому что
// старый parsePrice просто выдёргивал ВСЕ цифры подряд, не отличая
// разделитель копеек от обычной цифры суммы — "9 002,40" давало
// цифры "9", "0", "0", "2", "4", "0" подряд = 900240 вместо 9002.
// Теперь сначала вырезаем ",40"/".40" на конце числа (оставляя то,
// что шло после, например " ₽"), и только потом считаем цифры.
var kopecksSuffixPattern = regexp.MustCompile(`[.,]\d{2}(\D*)$`)

func parsePrice(
	raw string,
) *int64 {

	raw = strings.TrimSpace(raw)

	if raw == "" {
		return nil
	}

	raw = kopecksSuffixPattern.ReplaceAllString(raw, "$1")

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
