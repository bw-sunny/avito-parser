FROM golang:1.24-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /app/avito-parser ./cmd/api


FROM debian:bookworm-slim

WORKDIR /app

# Chromium и необходимые зависимости для запуска браузера
RUN apt-get update && apt-get install -y \
    chromium \
    ca-certificates \
    fonts-liberation \
    fonts-noto-color-emoji \
    libnss3 \
    libatk-bridge2.0-0 \
    libgtk-3-0 \
    libgbm1 \
    libasound2 \
    libx11-6 \
    libx11-xcb1 \
    libxcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libxshmfence1 \
    libdrm2 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/avito-parser /app/avito-parser

EXPOSE 8080

CMD ["/app/avito-parser"]
