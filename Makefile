.PHONY: run test audit build stress
run:
	go run .
test:
	go test ./...
audit:
	go vet ./...
	go test -race -cover ./...
build:
	go build -trimpath -o bin/tour-track .
stress:
	go run ./cmd/stress -requests 5000 -concurrency 32
