package parser

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"avito-parser/internal/browser"
	"avito-parser/internal/models"

	"github.com/chromedp/chromedp"
)

// =================================================================
// ВАЖНО, ПРОЧИТАТЬ ПЕРЕД ТЕМ, КАК ВКЛЮЧАТЬ ЭТОТ ПАРСЕР В ПРОД
//
// autodoc.ru/robots.txt явно запрещает автоматический доступ к
// странице поиска (Disallow). В отличие от Avito/Drom, где проблема
// чисто техническая (капча/рейт-лимиты), здесь это прямой,
// машиночитаемый запрет владельца сайта на этот конкретный путь —
// капча тут не покажется, пройти нечего, браузер просто не должен
// туда ходить.
//
// Юридическую оценку я дать не могу — не юрист — но технически
// нарушение robots.txt расценивается строже, чем просто сложность
// парсинга, и может быть использовано как доказательство
// недобросовестного доступа в споре.
//
// По вашей просьбе парсер всё равно подключён к общему браузеру и
// детектору капчи — на случай, если захотите попробовать сами. Но
// по умолчанию он ОТКЛЮЧЁН: включается явной переменной окружения
// AUTODOC_IGNORE_ROBOTS=true (см. main.go), а не кодом. Если решите
// включить — делайте это осознанно, у себя, под своим риском.
// =================================================================

// TODO: селекторы не проверены вживую — мой веб-инструмент получил
// ROBOTS_DISALLOWED при попытке открыть страницу поиска, так что
// я не видел реальную разметку. Контейнер карточки и поля ниже —
// предположение по типичной структуре интернет-магазинов, не факт.
const (
	autodocResultsContainerSelector = `[data-marker="product-list"]` // TODO: проверить
	autodocCardSelector             = `[data-marker="product-card"]` // TODO: проверить
)

type AutoDocParser struct {
	browser *browser.Manager

	// enabled — осознанное исключение из общего правила "не ходить
	// туда, где явно запрещено". Выставляется только через
	// AUTODOC_IGNORE_ROBOTS=true в main.go.
	enabled bool
}

type autodocRawListing struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	Href       string `json:"href"`
	Price      string `json:"price"`
	Image      string `json:"image"`
	Brand      string `json:"brand"`
}

func NewAutoDocParser(b *browser.Manager, enabled bool) *AutoDocParser {
	return &AutoDocParser{
		browser: b,
		enabled: enabled,
	}
}

func (p *AutoDocParser) Name() string {
	return "autodoc"
}

func (p *AutoDocParser) Search(
	ctx context.Context,
	params SearchParams,
) ([]models.Listing, error) {

	if !p.enabled {
		return nil, fmt.Errorf(
			"autodoc: поиск отключён (AUTODOC_IGNORE_ROBOTS!=true) — " +
				"путь запрещён в robots.txt, см. комментарий в начале " +
				"internal/parser/autodoc.go",
		)
	}

	params.Query = strings.TrimSpace(params.Query)

	if params.Query == "" {
		return nil, fmt.Errorf("search query is empty")
	}

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

	searchURL := buildAutoDocSearchURL(params.Query)

	fmt.Printf(
		"🔎 AutoDoc search: %s\n",
		searchURL,
	)

	var rawListings []autodocRawListing

	err := p.browser.Tab(ctx, SearchTabTimeout, func(searchCtx context.Context) error {

		err := chromedp.Run(
			searchCtx,
			chromedp.Navigate(searchURL),
			chromedp.Sleep(5*time.Second),
		)

		if err != nil {
			return fmt.Errorf("open AutoDoc search page: %w", err)
		}

		isCaptcha, err := browser.DetectCaptcha(searchCtx)

		if err != nil {
			return fmt.Errorf("detect captcha: %w", err)
		}

		if isCaptcha {

			if err := waitOrFailCaptcha(p.browser, searchCtx, "AutoDoc"); err != nil {
				return err
			}
		}

		err = chromedp.Run(
			searchCtx,
			chromedp.WaitVisible(
				autodocResultsContainerSelector,
				chromedp.ByQuery,
			),
		)

		if err != nil {
			return fmt.Errorf("wait AutoDoc search results: %w", err)
		}

		script := fmt.Sprintf(`
		(() => {

			const result = [];

			const cards = document.querySelectorAll(%q);

			for (const card of cards) {

				// TODO: заменить на реальные селекторы из DevTools.
				const titleElement = card.querySelector('[itemprop="name"]');
				const title = titleElement ? titleElement.textContent.trim() : '';

				const linkElement = card.querySelector('a');
				const href = linkElement ? linkElement.href : '';

				const priceElement = card.querySelector('[itemprop="price"]');
				const price = priceElement
					? (priceElement.getAttribute('content') || priceElement.textContent.trim())
					: '';

				const imageElement = card.querySelector('img');
				const image = imageElement
					? (imageElement.src || imageElement.getAttribute('src') || '')
					: '';

				// В каталоге магазина у товара обычно есть явный бренд
				// производителя детали (Bosch, Brembo...) — в отличие
				// от объявлений частников это value, не угадывание.
				const brandElement = card.querySelector('[itemprop="brand"]');
				const brand = brandElement ? brandElement.textContent.trim() : '';

				let externalId = '';
				const skuElement = card.querySelector('[data-sku]');
				if (skuElement) {
					externalId = skuElement.getAttribute('data-sku') || '';
				}
				if (!externalId && href) {
					const match = href.match(/(\d+)(?:\/|\?|$)/);
					if (match) {
						externalId = match[1];
					}
				}

				if (title || href) {
					result.push({ externalId, title, href, price, image, brand });
				}
			}

			return result;

		})()
		`, autodocCardSelector)

		return chromedp.Run(
			searchCtx,
			chromedp.Evaluate(script, &rawListings),
		)
	})

	if err != nil {
		return nil, fmt.Errorf("autodoc: %w", err)
	}

	if len(rawListings) == 0 {
		return []models.Listing{}, nil
	}

	listings := make(
		[]models.Listing,
		0,
		len(rawListings),
	)

	for _, raw := range rawListings {

		externalID := strings.TrimSpace(raw.ExternalID)
		href := strings.TrimSpace(raw.Href)

		if externalID == "" || href == "" {
			continue
		}

		now := time.Now()

		title := strings.TrimSpace(raw.Title)
		brand := strings.TrimSpace(raw.Brand)

		// В каталоге магазина бренд детали — отдельное,
		// надёжное поле, а не слово, которое надо выцепить из
		// заголовка эвристикой relevance.go. Добавляем его в
		// начало названия, чтобы partBrands его всё равно нашёл
		// при подсчёте релевантности, как и для остальных
		// источников.
		if brand != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(brand)) {
			title = brand + " " + title
		}

		listing := models.Listing{
			// SourceID проставляет SearchService после парсинга.
			ExternalID:  externalID,
			Title:       title,
			Description: "",
			Price:       parsePrice(raw.Price),
			Currency:    "RUB",
			URL:         href,
			City:        "",
			Region:      "",
			SellerType:  "shop",
			Condition:   "new",
			IsAvailable: true,
			ParsedAt:    now,
			UpdatedAt:   now,
		}

		listings = append(listings, listing)

		if len(listings) >= limit {
			break
		}
	}

	fmt.Printf(
		"✅ AutoDoc: найдено %d объявлений\n",
		len(listings),
	)

	return listings, nil
}

func buildAutoDocSearchURL(query string) string {

	query = strings.TrimSpace(query)
	encodedQuery := url.QueryEscape(query)

	return fmt.Sprintf(
		"https://www.autodoc.ru/search?keyword=%s",
		encodedQuery,
	)
}
