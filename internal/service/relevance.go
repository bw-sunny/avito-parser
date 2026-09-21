package service

import (
	"sort"
	"strings"
	"unicode"

	"avito-parser/internal/models"
)

// CalculateRelevance рассчитывает релевантность объявления
// относительно поискового запроса.
//
// Чем выше значение — тем больше объявление соответствует запросу.
func CalculateRelevance(
	listing models.Listing,
	query string,
) int {

	queryWords := normalizeWords(query)
	titleWords := normalizeWords(listing.Title)

	if len(queryWords) == 0 || len(titleWords) == 0 {
		return 0
	}

	titleSet := make(map[string]struct{}, len(titleWords))

	for _, word := range titleWords {
		titleSet[word] = struct{}{}
	}

	score := 0
	matchedWords := 0

	// =========================================================
	// 1. ТОЧНЫЕ СОВПАДЕНИЯ
	// =========================================================

	for _, queryWord := range queryWords {

		if _, exists := titleSet[queryWord]; exists {

			matchedWords++

			// Обычное совпадение.
			score += 10

			// Важные автомобильные сущности имеют больший вес.
			switch {
			case isCarBrand(queryWord):
				score += 15

			case isCarModelToken(queryWord):
				score += 25

			case isPartWord(queryWord):
				score += 20

			case isBrandName(queryWord):
				score += 20

			case isConditionWord(queryWord):
				score += 5
			}
		}
	}

	// =========================================================
	// 2. ВСЕ СЛОВА НАЙДЕНЫ
	// =========================================================

	if matchedWords == len(queryWords) {
		score += 50
	}

	// =========================================================
	// 3. ТОЧНАЯ ФРАЗА
	// =========================================================

	normalizedQuery := strings.Join(
		queryWords,
		" ",
	)

	normalizedTitle := strings.Join(
		titleWords,
		" ",
	)

	if normalizedQuery != "" &&
		strings.Contains(normalizedTitle, normalizedQuery) {

		score += 70
	}

	// =========================================================
	// 4. ЧАСТИЧНЫЕ СОВПАДЕНИЯ
	// =========================================================

	for _, queryWord := range queryWords {

		if len(queryWord) < 3 {
			continue
		}

		for _, titleWord := range titleWords {

			if titleWord == queryWord {
				continue
			}

			if strings.HasPrefix(titleWord, queryWord) ||
				strings.HasPrefix(queryWord, titleWord) {

				score += 4

				break
			}
		}
	}

	// =========================================================
	// 5. ШТРАФ ЗА ОТСУТСТВУЮЩИЕ СЛОВА
	// =========================================================

	missingWords := len(queryWords) - matchedWords

	if missingWords > 0 {

		for _, queryWord := range queryWords {

			if _, exists := titleSet[queryWord]; exists {
				continue
			}

			// Отсутствие модели особенно критично.
			if isCarModelToken(queryWord) {
				score -= 35
				continue
			}

			// Отсутствие марки тоже критично.
			if isCarBrand(queryWord) {
				score -= 30
				continue
			}

			// Отсутствие типа детали критично.
			if isPartWord(queryWord) {
				score -= 35
				continue
			}

			// Отсутствие бренда детали.
			if isBrandName(queryWord) {
				score -= 25
				continue
			}

			// Остальные слова.
			score -= 10
		}
	}

	// =========================================================
	// 6. ШТРАФ ЗА КОНФЛИКТ МОДЕЛЕЙ
	// =========================================================

	queryModels := make(map[string]struct{})

	for _, word := range queryWords {

		if isCarModelToken(word) {
			queryModels[word] = struct{}{}
		}
	}

	for _, titleWord := range titleWords {

		if !isCarModelToken(titleWord) {
			continue
		}

		if _, exists := queryModels[titleWord]; exists {
			continue
		}

		// Другая модель/поколение.
		score -= 20
	}

	// =========================================================
	// 7. ТИП ДЕТАЛИ
	// =========================================================

	queryHasPads := containsAny(
		queryWords,
		"колодки",
		"колодка",
	)

	queryHasDiscs := containsAny(
		queryWords,
		"диск",
		"диски",
		"дисков",
	)

	titleHasPads := containsAny(
		titleWords,
		"колодки",
		"колодка",
	)

	titleHasDiscs := containsAny(
		titleWords,
		"диск",
		"диски",
		"дисков",
	)

	// Ищем колодки, получили диск.
	if queryHasPads && titleHasDiscs && !titleHasPads {
		score -= 60
	}

	// Ищем диски, получили колодки.
	if queryHasDiscs && titleHasPads && !titleHasDiscs {
		score -= 60
	}

	// =========================================================
	// 8. СОСТОЯНИЕ
	// =========================================================

	queryNew := containsAny(
		queryWords,
		"новые",
		"новый",
		"новая",
		"новое",
	)

	titleNew := containsAny(
		titleWords,
		"новые",
		"новый",
		"новая",
		"новое",
	)

	if queryNew && !titleNew {
		score -= 20
	}

	// =========================================================
	// 9. БРЕНД ДЕТАЛИ
	// =========================================================

	for _, queryWord := range queryWords {

		if !isBrandName(queryWord) {
			continue
		}

		if _, exists := titleSet[queryWord]; !exists {
			score -= 30
		}
	}

	// =========================================================
	// 10. НЕГАТИВНЫЙ РЕЗУЛЬТАТ
	// =========================================================

	if score < 0 {
		score = 0
	}

	return score
}

// =============================================================
// SORT
// =============================================================

// SortByRelevance сортирует объявления по релевантности.
//
// При одинаковом рейтинге сохраняется исходный порядок.
func SortByRelevance(
	listings []models.Listing,
	query string,
) {

	for i := range listings {

		listings[i].Relevance = CalculateRelevance(
			listings[i],
			query,
		)
	}

	sort.SliceStable(
		listings,
		func(i, j int) bool {
			return listings[i].Relevance >
				listings[j].Relevance
		},
	)
}

// =============================================================
// HELPERS
// =============================================================

// normalizeWords приводит строку к набору слов.
//
// Например:
//
// "BMW E90 тормозные колодки"
//
// превращается в:
//
// ["bmw", "e90", "тормозные", "колодки"]
func normalizeWords(
	value string,
) []string {

	value = strings.ToLower(
		strings.TrimSpace(value),
	)

	value = strings.Map(
		func(r rune) rune {

			if unicode.IsLetter(r) ||
				unicode.IsDigit(r) {

				return r
			}

			return ' '
		},
		value,
	)

	return strings.Fields(value)
}

// =============================================================
// CONTAINS
// =============================================================

func containsAny(
	words []string,
	targets ...string,
) bool {

	set := make(map[string]struct{}, len(words))

	for _, word := range words {
		set[word] = struct{}{}
	}

	for _, target := range targets {

		if _, exists := set[target]; exists {
			return true
		}
	}

	return false
}

// =============================================================
// CAR BRANDS
// =============================================================

func isCarBrand(
	word string,
) bool {

	switch word {

	case "bmw",
		"audi",
		"mercedes",
		"mercedes-benz",
		"toyota",
		"lada",
		"ваз",
		"volkswagen",
		"volvo",
		"ford",
		"kia",
		"hyundai",
		"skoda",
		"nissan",
		"mazda",
		"honda",
		"lexus",
		"infiniti",
		"mitsubishi",
		"subaru",
		"chevrolet",
		"renault",
		"peugeot",
		"citroen",
		"opel",
		"porsche":
		return true
	}

	return false
}

// =============================================================
// PART BRANDS
// =============================================================

func isBrandName(
	word string,
) bool {

	switch word {

	case "brembo",
		"bosch",
		"textar",
		"ate",
		"trw",
		"ferodo",
		"mann",
		"mahle",
		"sachs",
		"luk",
		"lemforder",
		"ngk",
		"denso",
		"contitech",
		"gates":
		return true
	}

	return false
}

// =============================================================
// PART WORDS
// =============================================================

func isPartWord(
	word string,
) bool {

	switch word {

	case "колодки",
		"колодка",
		"диск",
		"диски",
		"дисков",
		"фильтр",
		"фильтры",
		"масло",
		"радиатор",
		"амортизатор",
		"амортизаторы",
		"ступица",
		"подшипник",
		"суппорт",
		"ремень",
		"свечи",
		"свеча",
		"насос",
		"стартер",
		"генератор",
		"бампер",
		"фара",
		"фары",
		"капот",
		"дверь",
		"двери":
		return true
	}

	return false
}

// =============================================================
// CONDITION
// =============================================================

func isConditionWord(
	word string,
) bool {

	switch word {

	case "новый",
		"новые",
		"новая",
		"новое",
		"оригинал",
		"оригинальный",
		"оригинальная",
		"оригинальное",
		"б/у",
		"бу":
		return true
	}

	return false
}

// =============================================================
// CAR MODEL
// =============================================================

// isCarModelToken определяет,
// похож ли токен на обозначение модели/поколения.
//
// Примеры:
//
// E90
// E60
// F30
// G20
// X5
// XV50
func isCarModelToken(
	word string,
) bool {

	if len(word) < 2 {
		return false
	}

	hasLetter := false
	hasDigit := false

	for _, r := range word {

		if unicode.IsLetter(r) {
			hasLetter = true
		}

		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}

	return hasLetter && hasDigit
}
