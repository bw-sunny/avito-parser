package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"avito-parser/internal/browser"
	"avito-parser/internal/database"
	"avito-parser/internal/handler"
	"avito-parser/internal/parser"
	"avito-parser/internal/repository"
	"avito-parser/internal/service"
)

func main() {

	// =========================
	// DATABASE
	// =========================

	db, err := database.Connect()
	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	log.Println("✅ Успешное подключение к PostgreSQL!")

	// =========================
	// REPOSITORIES
	// =========================

	listingRepository := repository.NewListingRepository(db)

	searchQueryRepository := repository.NewSearchQueryRepository(db)

	sourceRepository := repository.NewSourceRepository(db)

	searchQueryResultRepository :=
		repository.NewSearchQueryResultRepository(db)

	// =========================
	// BROWSER CONFIG
	// =========================

	browserPath := os.Getenv("BROWSER_PATH")

	if browserPath == "" {
		browserPath = "/usr/bin/chromium"
	}

	headless := getEnvBool("HEADLESS", true)

	log.Printf(
		"🌐 Browser: %s",
		browserPath,
	)

	log.Printf(
		"🖥️ Headless: %t",
		headless,
	)

	// =========================
	// BROWSER MANAGER
	//
	// Один процесс Chromium на всё приложение вместо нового
	// процесса на каждый поиск — см. internal/browser/manager.go.
	// Это нужно, чтобы куки пройденной капчи переживали отдельный
	// запрос, а не умирали вместе с ним.
	// =========================

	userDataDir := os.Getenv("BROWSER_USER_DATA_DIR")

	if userDataDir == "" {
		log.Println(
			"⚠️ BROWSER_USER_DATA_DIR не задан — профиль Chromium " +
				"(и куки пройденной капчи) не переживут рестарт процесса",
		)
	}

	maxTabs := getEnvInt("BROWSER_MAX_TABS", 2)

	browserManager, err := browser.NewManager(browser.Options{
		BrowserPath:       browserPath,
		Headless:          headless,
		UserDataDir:       userDataDir,
		MaxConcurrentTabs: maxTabs,
	})

	if err != nil {
		log.Fatal(err)
	}

	defer browserManager.Close()

	// =========================
	// PARSERS
	// =========================

	avitoParser := parser.NewAvitoParser(
		browserManager,
	)

	dromParser := parser.NewDromParser(
		browserManager,
	)

	// AutoDoc: его страница поиска запрещена в robots.txt (см.
	// internal/parser/autodoc.go) — это не капча, которую можно
	// пройти, а прямой запрет, который детектор капчи не обходит.
	// По умолчанию выключен; включается осознанно одной переменной.
	autodocEnabled := getEnvBool("AUTODOC_IGNORE_ROBOTS", false)

	if autodocEnabled {
		log.Println(
			"⚠️ AUTODOC_IGNORE_ROBOTS=true — AutoDoc будет парситься " +
				"несмотря на запрет в robots.txt, это осознанный риск",
		)
	}

	autodocParser := parser.NewAutoDocParser(
		browserManager,
		autodocEnabled,
	)

	// =========================
	// SEARCH SERVICE
	// =========================

	searchService := service.NewSearchService(
		listingRepository,
		sourceRepository,
		searchQueryRepository,
		searchQueryResultRepository,
		avitoParser,
		dromParser,
		autodocParser,
	)
	// =========================
	// HTTP HANDLER
	// =========================

	searchHandler := handler.NewSearchHandler(
		searchService,
	)

	mux := http.NewServeMux()

	mux.HandleFunc(
		"/api/search",
		searchHandler.Search,
	)

	mux.HandleFunc(
		"/health",
		func(w http.ResponseWriter, r *http.Request) {

			w.Header().Set(
				"Content-Type",
				"application/json",
			)

			w.WriteHeader(http.StatusOK)

			_, _ = w.Write(
				[]byte(`{"status":"ok"}`),
			)
		},
	)

	// =========================
	// HTTP SERVER
	// =========================

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr: ":" + port,

		Handler: mux,

		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf(
		"🚀 API server started on :%s",
		port,
	)

	log.Println("GET /health")
	log.Println("GET /api/search?query=...")

	if err := server.ListenAndServe(); err != nil &&
		err != http.ErrServerClosed {

		log.Fatal(err)
	}
}

// =========================
// ENV HELPERS
// =========================

func getEnvBool(
	key string,
	defaultValue bool,
) bool {

	value := os.Getenv(key)

	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(value)

	if err != nil {
		log.Printf(
			"⚠️ Invalid %s=%q, using default: %t",
			key,
			value,
			defaultValue,
		)

		return defaultValue
	}

	return parsed
}

func getEnvInt(
	key string,
	defaultValue int,
) int {

	value := os.Getenv(key)

	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.Atoi(value)

	if err != nil {
		log.Printf(
			"⚠️ Invalid %s=%q, using default: %d",
			key,
			value,
			defaultValue,
		)

		return defaultValue
	}

	return parsed
}
