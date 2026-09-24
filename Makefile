.PHONY: build build-llmd test run run-llmd

build:
	go build -o spider-mail ./cmd/spider-mail

test:
	go test ./...

run:
	go run ./cmd/spider-mail

build-llmd:
	go build -o bin/llmd ./cmd/llmd

run-llmd:
	go run ./cmd/llmd
