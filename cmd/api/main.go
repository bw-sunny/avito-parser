package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"avito-parser/internal/database"
	"avito-parser/internal/handler"
	"avito-parser/internal/parser"
	"avito-parser/internal/repository"
	"avito-parser/internal/service"

	"github.com/chromedp/chromedp"
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
	// CHROMEDP
	// =========================

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],

		chromedp.ExecPath(browserPath),

		chromedp.Flag(
			"headless",
			headless,
		),

		chromedp.Flag(
			"disable-gpu",
			headless,
		),

		chromedp.Flag(
			"no-sandbox",
			true,
		),

		chromedp.Flag(
			"disable-dev-shm-usage",
			true,
		),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(
		context.Background(),
		opts...,
	)

	defer cancel()

	// =========================
	// AVITO PARSER
	// =========================

	avitoParser := parser.NewAvitoParser(
		allocCtx,
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
