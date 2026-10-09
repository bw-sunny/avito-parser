package parser

import (
	"context"
	"fmt"
	"time"

	"avito-parser/internal/browser"
	"avito-parser/internal/models"
)

type SearchParams struct {
	Query string

	City string

	Limit int
}

type Parser interface {
	Name() string

	Search(
		ctx context.Context,
		params SearchParams,
	) ([]models.Listing, error)
}

// captchaMaxWait — сколько ждать, пока человек пройдёт капчу руками
// в видимом (не headless) окне браузера, прежде чем сдаться.
const captchaMaxWait = 3 * time.Minute

// SearchTabTimeout — таймаут вкладки, которую парсеры передают в
// browser.Manager.Tab(). Должен с запасом перекрывать captchaMaxWait:
// если бы он был короче (как раньше — 90 секунд при капче до 3 минут
// ожидания), Tab() закрыл бы вкладку по собственному таймауту прямо
// во время ожидания капчи, не дав её пройти. Запас сверх captchaMaxWait
// — на саму загрузку страницы, скролл и извлечение данных после того,
// как капча пройдена.
const SearchTabTimeout = captchaMaxWait + 2*time.Minute

// waitOrFailCaptcha — общая для всех парсеров реакция на обнаруженную
// капчу/проверку "я не робот".
//
//   - Headless=false (виден браузер, HEADLESS=false при запуске) — есть
//     кому пройти капчу руками: ждём до captchaMaxWait, опрашивая
//     страницу через browser.WaitCaptchaResolved, и если за это время
//     капча исчезла — продолжаем поиск как ни в чём не бывало.
//   - Headless=true — ждать нечего, капчу никто не увидит: сразу
//     возвращаем browser.ErrCaptcha, как и раньше.
func waitOrFailCaptcha(
	b *browser.Manager,
	tabCtx context.Context,
	sourceName string,
) error {

	if b.Headless {
		fmt.Printf("🧩 %s: показана капча (headless, ждать некому)\n", sourceName)
		return browser.ErrCaptcha
	}

	fmt.Printf(
		"🧩 %s: показана капча — пройдите её в открытом окне браузера "+
			"(жду до %s)...\n",
		sourceName,
		captchaMaxWait,
	)

	if err := browser.WaitCaptchaResolved(tabCtx, captchaMaxWait); err != nil {
		return err
	}

	fmt.Printf("✅ %s: капча пройдена, продолжаю поиск\n", sourceName)

	return nil
}
