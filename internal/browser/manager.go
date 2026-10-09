// Package browser держит один долгоживущий Chromium на всё приложение
// и раздаёт из него вкладки под отдельные поиски.
//
// До этого пакета каждый Parser.Search() сам вызывал
// chromedp.NewContext(allocCtx), где allocCtx — это "сырой" контекст
// аллокатора (chromedp.NewExecAllocator). Для ExecAllocator это
// запускает НОВЫЙ процесс Chromium на каждый вызов: профиль
// (cookies, в том числе те, что подтверждают пройденную капчу)
// исчезает вместе с процессом сразу после поиска.
//
// Manager устраняет это: процесс Chromium запускается один раз при
// старте сервиса, а каждый Search() получает новую вкладку внутри
// того же процесса через chromedp.NewContext(rootCtx) — при условии,
// что rootCtx сам уже является "браузерным" контекстом (а не сырым
// аллокатором), это создаёт новую вкладку в уже работающем браузере,
// а не новый процесс. Куки, выставленные при прохождении капчи в
// одной вкладке, остаются доступны следующим вкладкам того же
// браузера.
//
// Если дополнительно задать UserDataDir на постоянный путь (см.
// Options.UserDataDir), профиль переживёт и перезапуск самого
// процесса Chromium — но для этого путь должен быть смонтирован как
// постоянный volume, иначе в контейнере он исчезнет при рестарте.
package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ErrCaptcha — антибот-страница вместо ожидаемого контента.
// Парсеры возвращают эту ошибку (через errors.Is), чтобы вызывающий
// код мог отличить "сайт нас попросил пройти капчу" от обычного сбоя.
var ErrCaptcha = fmt.Errorf("captcha page detected")

// captchaMarkers — куски текста, по которым антибот-страницы
// Avito/Drom узнаются в document.body.innerText. При подключении
// нового источника сюда можно добавить его собственные маркеры.
var captchaMarkers = []string{
	// Avito.
	"Доступ ограничен",
	"Подтвердите, что запросы отправляете вы",
	"проблема с IP",

	// Drom: страница с чекбоксом "Я не робот" + кнопкой "Продолжить"
	// (не текстовая капча с картинками, а подтверждение трафика —
	// но с точки зрения детектора это то же самое: ожидаемого
	// контента страницы нет, значит, впереди ручное вмешательство).
	"Вы не робот",
	"Поставьте отметку, чтобы продолжить",
}

type Options struct {
	// BrowserPath — путь к бинарнику Chromium.
	BrowserPath string

	// Headless — headless-режим. false только имеет смысл вместе
	// с видимым дисплеем (Xvfb+VNC на сервере) — иначе процесс
	// просто не запустится в контейнере без X-сервера.
	Headless bool

	// UserDataDir — путь к профилю Chromium. Пустая строка —
	// Chromium создаст временный профиль сам (поведение по
	// умолчанию до этого пакета, куки теряются между рестартами
	// процесса). Непустой путь, смонтированный как volume —
	// профиль и куки переживают рестарт контейнера.
	UserDataDir string

	// MaxConcurrentTabs — сколько вкладок могут парсить одновременно.
	// Это ограничение CPU/RAM одного Chromium-процесса на VPS, а не
	// произвольное число — см. комментарий в NewManager.
	MaxConcurrentTabs int
}

// Manager владеет одним браузером на весь процесс приложения.
type Manager struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc

	rootCtx    context.Context
	rootCancel context.CancelFunc

	// tabs ограничивает число одновременно открытых вкладок —
	// семафор на основе буферизованного канала.
	tabs chan struct{}

	// Headless — тот же флаг, что передали в Options при запуске.
	// Публичное поле (а не только внутренняя настройка аллокатора),
	// т.к. парсеры используют его, чтобы решить, что делать при
	// обнаруженной капче: если браузер виден (false) — есть кому её
	// пройти руками, есть смысл ждать; если headless (true) — ждать
	// нечего, никто её не увидит, значит сразу сдаёмся с ErrCaptcha.
	Headless bool
}

// NewManager запускает Chromium один раз и возвращает Manager,
// готовый раздавать вкладки. Процесс живёт, пока не вызван Close()
// (обычно — до остановки сервиса).
func NewManager(opts Options) (*Manager, error) {

	if opts.MaxConcurrentTabs <= 0 {
		// На типичном VPS (1-2 vCPU, 1-2 ГБ RAM) больше 2-3
		// одновременных вкладок Chromium уже заметно нагружают
		// память — см. обсуждение в предыдущей переписке. Если
		// сервер мощнее, значение стоит поднять через конфиг,
		// а не хардкодить здесь.
		opts.MaxConcurrentTabs = 2
	}

	execOpts := append(
		chromedp.DefaultExecAllocatorOptions[:],

		chromedp.ExecPath(opts.BrowserPath),

		chromedp.Flag("headless", opts.Headless),
		chromedp.Flag("disable-gpu", opts.Headless),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	if opts.UserDataDir != "" {
		execOpts = append(
			execOpts,
			chromedp.UserDataDir(opts.UserDataDir),
		)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(),
		execOpts...,
	)

	// Это и есть тот самый единственный запуск процесса Chromium.
	// rootCtx — браузерный контекст (не сырой аллокатор), поэтому
	// дальнейшие chromedp.NewContext(rootCtx) будут открывать новые
	// вкладки в этом же браузере, а не новые процессы.
	rootCtx, rootCancel := chromedp.NewContext(allocCtx)

	// Форсируем реальный запуск процесса сейчас, а не при первом
	// поиске — чтобы ошибка запуска (битый путь к бинарнику и т.п.)
	// всплыла при старте сервиса, а не на первом запросе пользователя.
	if err := chromedp.Run(rootCtx); err != nil {
		rootCancel()
		allocCancel()

		return nil, fmt.Errorf("launch shared browser: %w", err)
	}

	return &Manager{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		rootCtx:     rootCtx,
		rootCancel:  rootCancel,
		tabs:        make(chan struct{}, opts.MaxConcurrentTabs),
		Headless:    opts.Headless,
	}, nil
}

// Close останавливает общий браузер. Вызывать один раз при
// остановке сервиса (defer в main).
func (m *Manager) Close() {
	m.rootCancel()
	m.allocCancel()
}

// Tab резервирует слот (блокируясь, если все вкладки заняты),
// открывает новую вкладку в общем браузере и передаёт её в fn.
// Вкладка закрывается и слот освобождается независимо от того,
// как fn завершилась.
//
// ctx — контекст запроса (например, r.Context() из HTTP-хендлера):
// если клиент отменит запрос, вкладка закроется вместе с ним.
// timeout ограничивает поиск сверху, даже если клиент не отменял.
func (m *Manager) Tab(
	ctx context.Context,
	timeout time.Duration,
	fn func(tabCtx context.Context) error,
) error {

	select {
	case m.tabs <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}

	defer func() { <-m.tabs }()

	tabCtx, cancel := chromedp.NewContext(m.rootCtx)
	defer cancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, timeout)
	defer timeoutCancel()

	if ctx != nil {

		stop := make(chan struct{})
		defer close(stop)

		go func() {
			select {
			case <-ctx.Done():
				timeoutCancel()
			case <-stop:
			}
		}()
	}

	return fn(tabCtx)
}

// DetectCaptcha проверяет, не антибот-страница ли сейчас открыта во
// вкладке tabCtx, сравнивая видимый текст страницы с captchaMarkers.
// Вызывать сразу после Navigate, до ожидания целевых селекторов —
// иначе WaitVisible на капче просто откатится по таймауту с невнятной
// ошибкой вместо чёткого ErrCaptcha.
func DetectCaptcha(tabCtx context.Context) (bool, error) {

	var pageText string

	err := chromedp.Run(
		tabCtx,
		chromedp.Evaluate(
			`document.body ? document.body.innerText : ''`,
			&pageText,
		),
	)

	if err != nil {
		return false, fmt.Errorf("read page text: %w", err)
	}

	for _, marker := range captchaMarkers {
		if strings.Contains(pageText, marker) {
			return true, nil
		}
	}

	return false, nil
}

// WaitCaptchaResolved опрашивает DetectCaptcha раз в captchaPollInterval,
// пока капча не исчезнет (человек поставил галочку/прошёл проверку в
// видимом окне браузера) или не истечёт maxWait.
//
// Имеет смысл вызывать только когда есть, кому проходить капчу — то
// есть при Manager.Headless == false. В headless-режиме её никто не
// увидит, ждать нечего, там парсеры сразу возвращают ErrCaptcha.
//
// tabCtx не отменяется и не закрывается здесь — вкладка, которую видит
// человек, должна оставаться открытой всё время ожидания; этим
// занимается вызывающий код (parser.Search через Manager.Tab).
func WaitCaptchaResolved(
	tabCtx context.Context,
	maxWait time.Duration,
) error {

	const captchaPollInterval = 2 * time.Second

	deadline := time.Now().Add(maxWait)

	for {

		isCaptcha, err := DetectCaptcha(tabCtx)

		if err != nil {
			return fmt.Errorf("poll captcha: %w", err)
		}

		if !isCaptcha {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf(
				"%w: не пройдена за %s",
				ErrCaptcha,
				maxWait,
			)
		}

		select {
		case <-tabCtx.Done():
			return tabCtx.Err()
		case <-time.After(captchaPollInterval):
		}
	}
}
