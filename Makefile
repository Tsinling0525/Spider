.PHONY: build test run

build:
	go build -o spider-mail ./cmd/spider-mail

test:
	go test ./...

run:
	go run ./cmd/spider-mail
