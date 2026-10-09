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
// СЕЛЕКТОРЫ ПОДТВЕРЖДЕНЫ ПО РЕАЛЬНОЙ РАЗМЕТКЕ
//
// Взяты из outerHTML настоящей карточки объявления на baza.drom.ru
// (не data-ftid, как было в первой версии-заглушке — у Drom обычные
// BEM-классы bull-item__* и data-role).
//
// Не проверено: URL страницы поиска (buildDromSearchURL ниже) —
// HTML карточки не говорит, с какого адреса её открыли. Если после
// первого реального запуска окажется, что поиск ведёт не туда,
// пришлите URL из адресной строки браузера после обычного поиска
// детали на сайте.
// =================================================================

const (
	// dromCardSelector — корневой div одной карточки объявления.
	// data-good-id надёжнее класса bull-item: это чистый числовой
	// ID объявления, не задействован в вёрстке/стилях, поэтому
	// меньше шансов, что редизайн его уберёт.
	dromCardSelector = `[data-good-id]`
)

type DromParser struct {
	browser *browser.Manager
}

type dromRawListing struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	Href       string `json:"href"`
	Price      string `json:"price"`
	Image      string `json:"image"`
	City       string `json:"city"`
}

func NewDromParser(b *browser.Manager) *DromParser {
	return &DromParser{
		browser: b,
	}
}

func (p *DromParser) Name() string {
	return "drom"
}

func (p *DromParser) Search(
	ctx context.Context,
	params SearchParams,
) ([]models.Listing, error) {

	// =========================================================
	// VALIDATION
	// =========================================================

	params.Query = strings.TrimSpace(params.Query)

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

	if p.browser == nil {
		return nil, fmt.Errorf("browser manager is nil")
	}

	// =========================================================
	// SEARCH URL
	// =========================================================

	searchURL := buildDromSearchURL(params.Query)

	fmt.Printf(
		"🔎 Drom search: %s\n",
		searchURL,
	)

	// =========================================================
	// OPEN DROM + EXTRACT
	// =========================================================

	var rawListings []dromRawListing

	err := p.browser.Tab(ctx, SearchTabTimeout, func(searchCtx context.Context) error {

		err := chromedp.Run(
			searchCtx,
			chromedp.Navigate(searchURL),
			chromedp.Sleep(5*time.Second),
		)

		if err != nil {
			return fmt.Errorf("open Drom search page: %w", err)
		}

		// См. комментарий в avito.go: капчу проверяем до
		// ожидания целевого селектора, иначе WaitVisible просто
		// откатится по таймауту с невнятной ошибкой.
		isCaptcha, err := browser.DetectCaptcha(searchCtx)

		if err != nil {
			return fmt.Errorf("detect captcha: %w", err)
		}

		if isCaptcha {

			if err := waitOrFailCaptcha(p.browser, searchCtx, "Drom"); err != nil {
				return err
			}
		}

		// Ждём появления любой карточки — отдельный селектор
		// контейнера выдачи не нужен (его разметки у нас нет,
		// а первой найденной карточки достаточно, чтобы понять,
		// что результаты поиска подгрузились).
		err = chromedp.Run(
			searchCtx,
			chromedp.WaitVisible(
				dromCardSelector,
				chromedp.ByQuery,
			),
		)

		if err != nil {
			return fmt.Errorf("wait Drom search results: %w", err)
		}

		// =====================================================
		// EXTRACT DOM
		// =====================================================

		script := fmt.Sprintf(`
		(() => {

			const result = [];

			const cards = document.querySelectorAll(%q);

			for (const card of cards) {

				const titleElement = card.querySelector('a.bulletinLink');
				const title = titleElement ? titleElement.textContent.trim() : '';

				// .href (DOM-свойство, не getAttribute) сам
				// достраивает абсолютный URL из относительного
				// href="/podolsk/..." в разметке.
				const href = titleElement ? titleElement.href : '';

				const priceElement = card.querySelector('[data-role="price"]');
				const price = priceElement ? priceElement.textContent.trim() : '';

				const imageElement = card.querySelector('img');
				const image = imageElement
					? (imageElement.src || imageElement.getAttribute('src') || '')
					: '';

				const cityElement = card.querySelector('.bull-delivery__city');
				const city = cityElement ? cityElement.textContent.trim() : '';

				let externalId = card.getAttribute('data-good-id') || '';

				if (!externalId && href) {
					const match = href.match(/g(\d+)\.html/);
					if (match) {
						externalId = match[1];
					}
				}

				if (title || href) {
					result.push({ externalId, title, href, price, image, city });
				}
			}

			return result;

		})()
		`, dromCardSelector)

		return chromedp.Run(
			searchCtx,
			chromedp.Evaluate(script, &rawListings),
		)
	})

	if err != nil {
		return nil, fmt.Errorf("drom: %w", err)
	}

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

		externalID := strings.TrimSpace(raw.ExternalID)
		href := strings.TrimSpace(raw.Href)

		if externalID == "" || href == "" {
			continue
		}

		now := time.Now()

		listing := models.Listing{
			// SourceID проставляет SearchService после парсинга.
			ExternalID:  externalID,
			Title:       strings.TrimSpace(raw.Title),
			Description: "",
			Price:       parsePrice(raw.Price),
			Currency:    "RUB",
			URL:         href,
			City:        strings.TrimSpace(raw.City),
			Region:      "",
			SellerType:  "",
			Condition:   "",
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
		"✅ Drom: найдено %d объявлений\n",
		len(listings),
	)

	return listings, nil
}

// =============================================================
// BUILD DROM SEARCH URL
//
// Региональный путь (/moskovskaya-obl/sell_spare_parts/...) — это
// фильтр по региону, а не обязательная часть адреса поиска. Без
// региона в пути (проверено через WebFetch: страница реальная, с
// объявлениями, не 404):
//
//	https://baza.drom.ru/sell_spare_parts/?query=<cp1251, percent-encoded>
//
// "/russia/" как регион не существует (пользователь словил 404) —
// общероссийский поиск это именно отсутствие регионального сегмента,
// а не особый код региона.
//
// Кодировка query — Windows-1251, не UTF-8. %F2%EE%F0%EC%EE%E7%ED%FB%E5
// в cp1251 — это "тормозные" (в UTF-8 кириллица кодировалась бы иначе,
// по 2 байта на символ). url.QueryEscape сам по себе кодировку не
// меняет, он percent-encode'ит уже готовые байты — поэтому сначала
// вручную переводим строку в cp1251 через encodeCP1251, и только потом
// отдаём результат в QueryEscape.
// =============================================================

func buildDromSearchURL(query string) string {

	query = strings.TrimSpace(query)

	encodedQuery := url.QueryEscape(encodeCP1251(query))

	return fmt.Sprintf(
		"https://baza.drom.ru/sell_spare_parts/?query=%s",
		encodedQuery,
	)
}

// encodeCP1251 переводит строку из UTF-8 в байты Windows-1251.
//
// Своя реализация вместо golang.org/x/text/encoding/charmap, чтобы не
// тянуть лишнюю зависимость ради одной таблицы: нам нужна только
// кириллица (plus ё/Ё), всё остальное (включая латиницу и цифры) в
// cp1251 совпадает с ASCII побайтово.
//
// Символ вне диапазона (которого в cp1251 нет) заменяется на '?' —
// для текста автозапчастей (бренды латиницей + русские слова) такого
// практически не бывает.
func encodeCP1251(s string) string {

	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {

		switch {
		case r < 0x80:
			// ASCII — совпадает с cp1251 побайтово.
			b.WriteByte(byte(r))

		case r == 0x401: // Ё
			b.WriteByte(0xA8)

		case r == 0x451: // ё
			b.WriteByte(0xB8)

		case r >= 0x410 && r <= 0x44F:
			// А-Я, а-я — в cp1251 это непрерывный блок 0xC0-0xFF,
			// сдвинутый относительно unicode-блока 0x410-0x44F.
			b.WriteByte(byte(r - 0x410 + 0xC0))

		default:
			b.WriteByte('?')
		}
	}

	return b.String()
}
