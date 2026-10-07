.PHONY: generate build run test e2e e2e-install screenshots lint tidy

generate:
	go tool templ generate
	@if [ -f sqlc.yaml ]; then go tool sqlc generate; fi

build: generate
	go build -o vinance ./cmd/vinance

run: generate
	go run ./cmd/vinance serve

test: generate
	go test ./...

e2e-install:
	go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium

e2e: generate
	go test -tags e2e ./e2e/...

# Regenerates docs/screenshots from synthetic demo data (never touches data/). Needs make e2e-install once.
screenshots: generate
	go run ./cmd/screenshots

lint:
	go vet ./...

tidy:
	go mod tidy
