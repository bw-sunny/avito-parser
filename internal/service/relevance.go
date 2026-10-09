package service

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"avito-parser/internal/models"
)

// Query — поисковый запрос, разделённый на две части:
//
//	Part — деталь, которую ввёл пользователь ("колодки передние");
//	Car  — автомобиль из гаража ("BMW 3 Series E90").
//
// Если в поиск уходит склеенная строка (fmt.Sprintf("%s %s", part, car)),
// можно использовать CalculateRelevance / SortByRelevance: они сами
// попробуют разделить строку на деталь и машину. Но точнее передавать
// Part и Car отдельно через CalculateRelevanceFor / SortByRelevanceFor.
type Query struct {
	Part string
	Car  string
}

// Итоговая оценка — число от 0 до 100.
//
// Структура оценки:
//
//	55%  деталь: совпала ли группа детали (колодки/диски/фильтр...)
//	     и уточнения (передние, тормозные, brembo, новые)
//	45%  машина: марка, модель, кузов/поколение (E90, F30...)
//
// Если машина не задана, вся оценка строится по детали.
//
// Затем применяются поправки и потолки: объявление про другую деталь
// или другую марку не может получить высокий балл, даже если остальные
// слова совпали.
const (
	partWeight = 0.55
	carWeight  = 0.45

	// Внутри детали: основная группа, бренд детали (brembo, bosch...)
	// и прочие уточнения (передние, новые...).
	criticalPartShare = 0.60
	partBrandShare    = 0.25
	modifierPartShare = 0.15

	// Внутри машины.
	brandShare      = 0.30
	modelShare      = 0.40
	generationShare = 0.30
)

// =============================================================
// PUBLIC API
// =============================================================

// CalculateRelevance оценивает объявление по склеенной строке запроса.
func CalculateRelevance(
	listing models.Listing,
	query string,
) int {
	return CalculateRelevanceFor(listing, splitRawQuery(query))
}

// SortByRelevance записывает Relevance в каждое объявление
// и сортирует список по убыванию (порядок равных сохраняется).
func SortByRelevance(
	listings []models.Listing,
	query string,
) {
	SortByRelevanceFor(listings, splitRawQuery(query))
}

// CalculateRelevanceFor оценивает объявление по структурированному запросу.
func CalculateRelevanceFor(
	listing models.Listing,
	q Query,
) int {
	return score(analyzeQuery(q), analyzeTitle(listing.Title))
}

// SortByRelevanceFor записывает Relevance в каждое объявление
// и сортирует список по убыванию (порядок равных сохраняется).
func SortByRelevanceFor(
	listings []models.Listing,
	q Query,
) {
	info := analyzeQuery(q)

	for i := range listings {
		listings[i].Relevance = score(
			info,
			analyzeTitle(listings[i].Title),
		)
	}

	sort.SliceStable(
		listings,
		func(i, j int) bool {
			return listings[i].Relevance > listings[j].Relevance
		},
	)
}

// =============================================================
// SCORE
// =============================================================

func score(q queryInfo, t titleInfo) int {

	hasPart := len(q.groups) > 0 || len(q.modifiers) > 0
	hasCar := len(q.brands) > 0 ||
		len(q.generations) > 0 ||
		len(q.models) > 0

	if (!hasPart && !hasCar) || len(t.order) == 0 {
		return 0
	}

	var raw float64

	switch {
	case hasPart && hasCar:
		raw = partWeight*partRatio(q, t) + carWeight*carRatio(q, t)
	case hasPart:
		raw = partRatio(q, t)
	default:
		raw = carRatio(q, t)
	}

	s := int(math.Round(raw * 100))

	// ---------------------------------------------------------
	// Поправки
	// ---------------------------------------------------------

	partFound := false

	for _, g := range q.groups {
		if _, ok := t.groups[g]; ok {
			partFound = true
			break
		}
	}

	// Деталь стоит в начале названия — это почти всегда сам товар
	// ("Колодки тормозные BMW E90"), а не упоминание в тексте.
	if partFound &&
		t.firstGroupPos >= 0 &&
		t.firstGroupPos <= 1 &&
		containsStr(q.groups, t.firstGroup) {

		s += 5
	}

	// Передние/задние, левые/правые, верхние/нижние.
	for _, pair := range oppositePairs {

		for k := 0; k < 2; k++ {

			want, other := pair[k], pair[1-k]

			_, titleHasWant := t.tokens[want]
			_, titleHasOther := t.tokens[other]

			if containsStr(q.modifiers, want) &&
				!containsStr(q.modifiers, other) &&
				titleHasOther && !titleHasWant {

				s -= 15
			}
		}
	}

	// Новое / б/у.
	_, titleNew := t.tokens["нов"]
	_, titleUsed := t.tokens["бу"]

	if containsStr(q.modifiers, "нов") && titleUsed && !titleNew {
		s -= 20
	}

	if containsStr(q.modifiers, "бу") && titleNew && !titleUsed {
		s -= 20
	}

	// Другое поколение/кузов. Штраф мягкий: в названии может
	// встретиться код двигателя (N47, M54), похожий на код кузова.
	if len(q.generations) > 0 &&
		len(t.generations) > 0 &&
		!anyGenerationMatch(q.generations, t) {

		s -= 15
	}

	// Поколение запрошено (например, E70), но в названии нет вообще
	// никакого кода кузова/поколения — ни совпадающего, ни другого
	// ("Датчик износа колодок BMW X6, X5" без уточнения). Это не
	// противоречие (штраф выше здесь не сработает — t.generations
	// пуст), но и не подтверждение: совпадение марки по одному слову
	// "bmw" не значит, что деталь подходит именно к запрошенному
	// кузову — она могла с тем же успехом быть для F15/G05. Раньше
	// такие объявления получали почти столько же баллов, сколько и
	// объявления с явно совпавшим кодом поколения.
	if len(q.generations) > 0 && len(t.generations) == 0 {
		s = minInt(s, 55)
	}

	// "Куплю", "ищу", "аренда" — это не товар.
	for tok := range t.tokens {
		if _, bad := noSaleStems[tok]; bad {
			s -= 40
			break
		}
	}

	// Конфликт подтипа: например, запрошены тормозные колодки,
	// а в названии — колодки стояночного тормоза (ручника). Группа
	// детали совпала формально, но это другой товар.
	for _, g := range q.groups {

		for _, conflict := range partSubtypeConflicts[g] {

			_, titleHasConflict := t.tokens[conflict]

			if titleHasConflict && !containsStr(q.modifiers, conflict) {
				s = minInt(s, 40)
			}
		}
	}

	// ---------------------------------------------------------
	// Потолки
	// ---------------------------------------------------------

	if len(q.groups) > 0 && !partFound {

		if len(t.groups) > 0 {
			// Ищем колодки, а это диски/фильтр/бампер.
			s = minInt(s, 15)
		} else {
			// Нужной детали в названии нет вообще
			// (например, продаётся сама машина).
			s = minInt(s, 30)
		}
	}

	// В названии есть другие марки, а нашей нет.
	if len(q.brands) > 0 &&
		len(t.carBrands) > 0 &&
		!anyIn(q.brands, t.carBrands) {

		s = minInt(s, 15)
	}

	// Запрошен конкретный бренд детали (brembo), а в названии —
	// другой известный бренд (bosch). Это не то, что просили,
	// даже если деталь и машина совпали.
	if len(q.partBrands) > 0 &&
		len(t.partBrands) > 0 &&
		!anyIn(q.partBrands, t.partBrands) {

		s = minInt(s, 40)
	}

	if s < 0 {
		s = 0
	}

	if s > 100 {
		s = 100
	}

	return s
}

func partRatio(q queryInfo, t titleInfo) float64 {

	var sum, weight float64

	if len(q.groups) > 0 {

		weight += criticalPartShare
		sum += criticalPartShare * fraction(q.groups, t.groups)
	}

	if len(q.partBrands) > 0 {

		weight += partBrandShare
		sum += partBrandShare * fraction(q.partBrands, t.partBrands)
	}

	if len(q.modifiers) > 0 {

		matched := 0

		for _, m := range q.modifiers {
			if hasTokenFuzzy(t.tokens, m) {
				matched++
			}
		}

		ratio := float64(matched) / float64(len(q.modifiers))

		weight += modifierPartShare
		sum += modifierPartShare * ratio
	}

	if weight == 0 {
		return 0
	}

	return sum / weight
}

func carRatio(q queryInfo, t titleInfo) float64 {

	var sum, weight float64

	if len(q.brands) > 0 {

		brandFraction := fraction(q.brands, t.tokens)

		// Марка не названа в тексте явно, но код кузова/поколения
		// (E90, XV50...) практически однозначно указывает на неё —
		// не наказываем объявление только за то, что оно не
		// повторило марку словами.
		if brandFraction == 0 && len(q.generations) > 0 {

			if genFraction := generationFraction(q.generations, t); genFraction > 0 {
				brandFraction = genFraction
			}
		}

		weight += brandShare
		sum += brandShare * brandFraction
	}

	if len(q.models) > 0 {
		weight += modelShare
		sum += modelShare * fraction(q.models, t.tokens)
	}

	if len(q.generations) > 0 {
		weight += generationShare
		sum += generationShare * generationFraction(q.generations, t)
	}

	if weight == 0 {
		return 0
	}

	return sum / weight
}

// generationFraction считает долю совпавших кодов кузова/поколения.
//
// Учитывает не только точное совпадение ("e90" == "e90"), но и голое
// число против полного кода ("50" засчитывается, если в названии есть
// "xv50" или "gf50" — оно оканчивается на эти цифры).
func generationFraction(queryGens []string, t titleInfo) float64 {

	if len(queryGens) == 0 {
		return 0
	}

	matched := 0

	for _, g := range queryGens {
		if matchesGeneration(g, t) {
			matched++
		}
	}

	return float64(matched) / float64(len(queryGens))
}

func anyGenerationMatch(queryGens []string, t titleInfo) bool {

	for _, g := range queryGens {
		if matchesGeneration(g, t) {
			return true
		}
	}

	return false
}

func matchesGeneration(g string, t titleInfo) bool {

	if _, ok := t.tokens[g]; ok {
		return true
	}

	if !isDigits(g) {
		return false
	}

	for code := range t.generations {

		if code != g && strings.HasSuffix(code, g) {
			return true
		}
	}

	return false
}

// =============================================================
// QUERY / TITLE ANALYSIS
// =============================================================

type queryInfo struct {
	groups      []string // группы деталей: "колодк", "диск"...
	partBrands  []string // бренды детали: brembo, bosch...
	modifiers   []string // уточнения: передн, нов...
	brands      []string // марки авто (канонические): bmw, audi...
	models      []string // слова модели: x5, camry, 2110...
	generations []string // коды кузова/поколения: e90, f30...
}

type titleInfo struct {
	order         []string
	tokens        map[string]struct{}
	groups        map[string]struct{}
	partBrands    map[string]struct{}
	carBrands     map[string]struct{}
	generations   map[string]struct{}
	firstGroup    string
	firstGroupPos int
}

func analyzeQuery(q Query) queryInfo {

	var info queryInfo

	for _, word := range normalize(q.Part) {

		if _, stop := partStopWords[word]; stop {
			continue
		}

		tok := token(word)

		if group, ok := partGroups[tok]; ok {
			info.groups = appendUnique(info.groups, group)
			continue
		}

		if _, ok := partBrands[tok]; ok {
			info.partBrands = appendUnique(info.partBrands, tok)
			continue
		}

		// Общие описательные слова ("тормозные", "ходовые") почти
		// всегда дублируют уже найденную группу детали и не несут
		// различительной силы, а их нечёткое сравнение по префиксу
		// (hasTokenFuzzy) может случайно совпасть с посторонним
		// словом из другой основы ("тормозн" ~ "тормоз" в "тормоза
		// стояночного"). Поэтому такие слова не становятся
		// уточнениями.
		if _, generic := genericPartAdjectives[tok]; generic {
			continue
		}

		info.modifiers = appendUnique(info.modifiers, tok)
	}

	for _, word := range normalize(q.Car) {

		if _, stop := carStopWords[word]; stop {
			continue
		}

		tok := token(word)

		if _, ok := carBrands[tok]; ok {
			info.brands = appendUnique(info.brands, tok)
			continue
		}

		if isGeneration(word) {
			info.generations = appendUnique(info.generations, word)
			continue
		}

		// Одиночный символ ("3" в "BMW 3 Series") ничего
		// не говорит о модели — отбрасываем.
		if len([]rune(word)) < 2 {
			continue
		}

		// Голое число из 2-3 цифр без буквы — это почти всегда
		// кузов/поколение в разговорном написании, а не год
		// и не часть названия модели.
		//
		// Например, Toyota Camry 50 (= XV50), Corolla 120, BMW 46
		// (= E46) часто пишут без буквенного префикса.
		if isDigits(word) && len(word) >= 2 && len(word) <= 3 {
			info.generations = appendUnique(info.generations, word)
			continue
		}

		info.models = appendUnique(info.models, tok)
	}

	return info
}

func analyzeTitle(title string) titleInfo {

	info := titleInfo{
		tokens:        make(map[string]struct{}),
		groups:        make(map[string]struct{}),
		partBrands:    make(map[string]struct{}),
		carBrands:     make(map[string]struct{}),
		generations:   make(map[string]struct{}),
		firstGroupPos: -1,
	}

	for _, word := range normalize(title) {

		tok := token(word)

		position := len(info.order)

		info.order = append(info.order, tok)
		info.tokens[tok] = struct{}{}

		if group, ok := partGroups[tok]; ok {

			info.groups[group] = struct{}{}

			if info.firstGroupPos < 0 {
				info.firstGroup = group
				info.firstGroupPos = position
			}
		}

		if _, ok := partBrands[tok]; ok {
			info.partBrands[tok] = struct{}{}
		}

		if _, ok := carBrands[tok]; ok {
			info.carBrands[tok] = struct{}{}
		}

		if isGeneration(word) {
			info.generations[word] = struct{}{}
		}

		// Голое число кузова/поколения без буквы ("Camry 50",
		// "Corolla 120") — см. комментарий в analyzeQuery.
		if isDigits(word) && len(word) >= 2 && len(word) <= 3 {
			info.generations[word] = struct{}{}
		}
	}

	return info
}

// splitRawQuery делит склеенную строку на деталь и машину.
//
// В машину попадают: марки, коды кузова, латиница и числа.
// Всё остальное (кириллица, названия деталей, "новые", "brembo"
// как исключение) считается деталью.
func splitRawQuery(raw string) Query {

	var part, car []string

	for _, word := range normalize(raw) {

		tok := token(word)

		if _, ok := carBrands[tok]; ok {
			car = append(car, word)
			continue
		}

		if _, ok := partBrands[tok]; ok {
			part = append(part, word)
			continue
		}

		if _, ok := partGroups[tok]; ok {
			part = append(part, word)
			continue
		}

		if isGeneration(word) || !hasCyrillic(word) {
			car = append(car, word)
			continue
		}

		part = append(part, word)
	}

	return Query{
		Part: strings.Join(part, " "),
		Car:  strings.Join(car, " "),
	}
}

// =============================================================
// TEXT HELPERS
// =============================================================

// normalize приводит строку к списку слов в нижнем регистре.
// "Колодки BMW E90/E91 (б/у)" -> [колодки bmw e90 e91 бу]
func normalize(value string) []string {

	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "ё", "е")
	value = strings.ReplaceAll(value, "б/у", "бу")
	value = strings.ReplaceAll(value, "б.у.", "бу")

	value = strings.Map(
		func(r rune) rune {

			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}

			return ' '
		},
		value,
	)

	return strings.Fields(value)
}

// token возвращает каноническую форму слова:
// бренд -> его каноническое имя ("бмв" -> "bmw"),
// русское слово -> основа ("колодки", "колодок" -> "колодк").
func token(word string) string {

	if canonical, ok := brandAliases[word]; ok {
		return canonical
	}

	return stem(word)
}

// stem — простой стеммер для русских слов: отрезает окончание,
// если после этого остаётся минимум 3 буквы. Латиница не меняется.
func stem(word string) string {

	if !hasCyrillic(word) {
		return word
	}

	if s, ok := irregularStems[word]; ok {
		return s
	}

	runes := []rune(word)

	for _, suffix := range stemSuffixes {

		suffixLen := len([]rune(suffix))

		if len(runes)-suffixLen >= 3 &&
			strings.HasSuffix(word, suffix) {

			return string(runes[:len(runes)-suffixLen])
		}
	}

	return word
}

func hasCyrillic(word string) bool {

	for _, r := range word {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}

	return false
}

func isDigits(word string) bool {

	if word == "" {
		return false
	}

	for _, r := range word {
		if !unicode.IsDigit(r) {
			return false
		}
	}

	return true
}

// generationPattern — код кузова/поколения: E90, F30, G20, XV50, W204.
var generationPattern = regexp.MustCompile(`^[a-z]{1,2}\d{2,3}$`)

// notGenerationPattern — похожие по форме, но не кузова:
// размер диска R16, цоколь лампы H11.
var notGenerationPattern = regexp.MustCompile(`^[rh]\d{1,2}$`)

func isGeneration(word string) bool {
	return generationPattern.MatchString(word) &&
		!notGenerationPattern.MatchString(word)
}

// hasTokenFuzzy ищет токен точно или по префиксу
// ("оригинал" найдёт "оригинальн").
func hasTokenFuzzy(tokens map[string]struct{}, want string) bool {

	if _, ok := tokens[want]; ok {
		return true
	}

	if len([]rune(want)) < 5 {
		return false
	}

	for tok := range tokens {
		if strings.HasPrefix(tok, want) ||
			(strings.HasPrefix(want, tok) && len([]rune(tok)) >= 5) {

			return true
		}
	}

	return false
}

func fraction(items []string, tokens map[string]struct{}) float64 {

	if len(items) == 0 {
		return 0
	}

	matched := 0

	for _, item := range items {
		if _, ok := tokens[item]; ok {
			matched++
		}
	}

	return float64(matched) / float64(len(items))
}

func appendUnique(items []string, value string) []string {

	if containsStr(items, value) {
		return items
	}

	return append(items, value)
}

func containsStr(items []string, value string) bool {

	for _, item := range items {
		if item == value {
			return true
		}
	}

	return false
}

func anyIn(items []string, set map[string]struct{}) bool {

	for _, item := range items {
		if _, ok := set[item]; ok {
			return true
		}
	}

	return false
}

func minInt(a, b int) int {

	if a < b {
		return a
	}

	return b
}

// =============================================================
// DICTIONARIES
// =============================================================

var stemSuffixes = []string{
	// 3 буквы
	"ыми", "ими", "его", "ого", "ому", "ему",
	// 2 буквы
	"ах", "ях", "ой", "ий", "ый", "ая", "яя", "ое", "ее",
	"ые", "ие", "ую", "юю", "ом", "ем", "ам", "ям",
	"ов", "ев", "ей", "их", "ых",
	// 1 буква
	"а", "я", "о", "е", "ы", "и", "у", "ю", "ь", "й",
}

// Формы, которые стеммер не приводит к основе
// (родительный падеж множественного числа с беглой гласной).
var irregularStems = map[string]string{
	"колодок":   "колодк",
	"масел":     "масл",
	"стоек":     "стойк",
	"втулок":    "втулк",
	"прокладок": "прокладк",
	"щеток":     "щетк",
	"форсунок":  "форсунк",
	"ламп":      "ламп",
	"фар":       "фар",
	"крыльев":   "крыль",
	"зеркал":    "зеркал",
	"дверей":    "двер",
}

// partGroups: основа слова -> группа детали.
// Слова одной группы считаются одной деталью,
// слова из разных групп — разными деталями.
//
// Известное ограничение: "диск" может быть тормозным или колёсным.
// Группа общая, поэтому "диски" найдёт и то и другое;
// различить их помогают уточнения ("тормозные", "литые", "r16").
var partGroups = buildPartGroups([]struct {
	group string
	stems []string
}{
	{"колодк", []string{"колодк"}},
	{"диск", []string{"диск"}},
	{"суппорт", []string{"суппорт"}},
	{"ступиц", []string{"ступиц"}},
	{"подшипник", []string{"подшипник"}},
	{"амортизатор", []string{"амортизатор"}},
	{"стойк", []string{"стойк"}},
	{"пружин", []string{"пружин"}},
	{"рычаг", []string{"рычаг"}},
	{"сайлентблок", []string{"сайлентблок"}},
	{"втулк", []string{"втулк"}},
	{"тяг", []string{"тяг"}},
	{"наконечник", []string{"наконечник"}},
	{"шрус", []string{"шрус"}},
	{"фильтр", []string{"фильтр"}},
	{"масл", []string{"масл"}},
	{"свеч", []string{"свеч"}},
	{"ремен", []string{"ремен", "ремн"}},
	{"насос", []string{"насос", "помп"}},
	{"стартер", []string{"стартер"}},
	{"генератор", []string{"генератор"}},
	{"радиатор", []string{"радиатор"}},
	{"вентилятор", []string{"вентилятор"}},
	{"термостат", []string{"термостат"}},
	{"бампер", []string{"бампер"}},
	{"фар", []string{"фар"}},
	{"фонар", []string{"фонар"}},
	{"капот", []string{"капот"}},
	{"двер", []string{"двер"}},
	{"крыл", []string{"крыл", "крыль"}},
	{"зеркал", []string{"зеркал"}},
	{"стекл", []string{"стекл"}},
	{"ламп", []string{"ламп"}},
	{"аккумулятор", []string{"аккумулятор", "акб"}},
	{"сцеплени", []string{"сцеплени"}},
	{"коробк", []string{"коробк", "кпп", "акпп", "мкпп"}},
	{"турбин", []string{"турбин"}},
	{"форсунк", []string{"форсунк"}},
	{"датчик", []string{"датчик"}},
	{"прокладк", []string{"прокладк"}},
	{"щетк", []string{"щетк"}},
	{"глушител", []string{"глушител"}},
	{"катализатор", []string{"катализатор"}},
	{"подкрылок", []string{"подкрылок"}},
	{"порог", []string{"порог"}},
	{"решетк", []string{"решетк"}},
})

func buildPartGroups(defs []struct {
	group string
	stems []string
}) map[string]string {

	result := make(map[string]string)

	for _, def := range defs {
		for _, s := range def.stems {
			result[s] = def.group
		}
	}

	return result
}

// genericPartAdjectives — общие описательные слова при деталях
// ("тормозные колодки", "ходовая часть"), которые почти всегда
// дублируют уже найденную группу детали. Они намеренно не участвуют
// в сравнении: иначе их нечёткое сравнение по префиксу (hasTokenFuzzy)
// может случайно совпасть с другим словом той же основы
// ("тормозн" ~ "тормоз" из "тормоза стояночного" — ручника,
// а не рабочей тормозной системы).
var genericPartAdjectives = buildStemSet(
	"тормозной", "тормозная", "тормозное", "тормозные",
	"ходовой", "ходовая", "ходовое", "ходовые",
	"рулевой", "рулевая", "рулевое", "рулевые",
	"моторный", "моторная", "моторное", "моторные",
)

// partSubtypeConflicts: группа детали -> основы слов, которые говорят,
// что это другая разновидность той же группы, а не то, что обычно
// имеют в виду под этим словом.
//
// Например, "колодки" почти всегда значит тормозные колодки рабочей
// тормозной системы, а не колодки стояночного тормоза (ручника).
var partSubtypeConflicts = map[string][]string{
	"колодк": {"стояночн", "ручник"},
}

// Слова-пары: если пользователь просит одно, а в названии другое.
var oppositePairs = [][2]string{
	{"передн", "задн"},
	{"лев", "прав"},
	{"верхн", "нижн"},
}

// Слова, которые не несут смысла для сравнения.
var partStopWords = map[string]struct{}{
	"для": {}, "на": {}, "и": {}, "в": {}, "с": {}, "по": {},
	"от": {}, "из": {}, "к": {}, "за": {}, "не": {},
	"комплект": {}, "шт": {},
}

var carStopWords = map[string]struct{}{
	"series": {}, "class": {}, "benz": {}, "generation": {}, "gen": {},
	"restyling": {}, "facelift": {}, "sedan": {}, "wagon": {},
	"серия": {}, "серии": {}, "класс": {}, "поколение": {},
	"рестайлинг": {}, "дорестайлинг": {},
	"седан": {}, "универсал": {}, "хэтчбек": {},
	"для": {}, "на": {}, "и": {}, "в": {}, "с": {},
}

// Объявления, которые не являются товаром.
var noSaleStems = buildStemSet(
	"куплю", "ищу", "скупка", "выкуп",
	"аренда", "прокат", "обмен",
)

func buildStemSet(words ...string) map[string]struct{} {

	result := make(map[string]struct{}, len(words))

	for _, w := range words {
		result[stem(w)] = struct{}{}
	}

	return result
}

// Алиасы марок автомобилей: написание -> каноническое имя.
var carBrandAliases = map[string]string{
	"bmw": "bmw", "бмв": "bmw",
	"audi": "audi", "ауди": "audi",
	"mercedes": "mercedes", "мерседес": "mercedes", "мерс": "mercedes",
	"volkswagen": "volkswagen", "vw": "volkswagen",
	"фольксваген": "volkswagen",
	"toyota":      "toyota", "тойота": "toyota",
	"lada": "lada", "лада": "lada", "vaz": "lada", "ваз": "lada",
	"ford": "ford", "форд": "ford",
	"kia": "kia", "киа": "kia",
	"hyundai": "hyundai", "хендай": "hyundai", "хундай": "hyundai",
	"хендэ": "hyundai", "хюндай": "hyundai",
	"skoda": "skoda", "шкода": "skoda",
	"nissan": "nissan", "ниссан": "nissan",
	"mazda": "mazda", "мазда": "mazda",
	"honda": "honda", "хонда": "honda",
	"lexus": "lexus", "лексус": "lexus",
	"infiniti": "infiniti", "инфинити": "infiniti",
	"mitsubishi": "mitsubishi", "митсубиси": "mitsubishi",
	"мицубиси": "mitsubishi",
	"subaru":   "subaru", "субару": "subaru",
	"chevrolet": "chevrolet", "шевроле": "chevrolet",
	"renault": "renault", "рено": "renault",
	"peugeot": "peugeot", "пежо": "peugeot",
	"citroen": "citroen", "ситроен": "citroen",
	"opel": "opel", "опель": "opel",
	"porsche": "porsche", "порше": "porsche",
	"volvo": "volvo", "вольво": "volvo",
	"suzuki": "suzuki", "сузуки": "suzuki",
	"daewoo": "daewoo", "дэу": "daewoo",
	"chery": "chery", "чери": "chery",
	"geely": "geely", "джили": "geely",
	"haval": "haval", "хавейл": "haval",
	"uaz": "uaz", "уаз": "uaz",
	"gaz": "gaz", "газ": "gaz",
}

// Алиасы производителей запчастей.
var partBrandAliases = map[string]string{
	"brembo": "brembo", "брембо": "brembo",
	"bosch": "bosch", "бош": "bosch",
	"textar": "textar", "ate": "ate", "trw": "trw",
	"ferodo": "ferodo", "mann": "mann", "mahle": "mahle",
	"sachs": "sachs", "luk": "luk", "lemforder": "lemforder",
	"ngk": "ngk", "denso": "denso", "contitech": "contitech",
	"gates": "gates", "febi": "febi", "hella": "hella",
	"valeo": "valeo", "kayaba": "kayaba", "monroe": "monroe",
	"bilstein": "bilstein", "zimmermann": "zimmermann",
}

var (
	brandAliases map[string]string
	carBrands    map[string]struct{}
	partBrands   map[string]struct{}
)

func init() {

	brandAliases = make(
		map[string]string,
		len(carBrandAliases)+len(partBrandAliases),
	)

	carBrands = make(map[string]struct{})
	partBrands = make(map[string]struct{})

	for alias, canonical := range carBrandAliases {
		brandAliases[alias] = canonical
		carBrands[canonical] = struct{}{}
	}

	for alias, canonical := range partBrandAliases {
		brandAliases[alias] = canonical
		partBrands[canonical] = struct{}{}
	}
}
