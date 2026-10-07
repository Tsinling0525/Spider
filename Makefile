.PHONY: build build-llmd test run run-llmd build-dashboardd run-dashboardd build-dashboard dev-dashboard

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

build-dashboardd:
	go build -o bin/dashboardd ./cmd/dashboardd

run-dashboardd:
	go run ./cmd/dashboardd

build-dashboard:
	npm --prefix dashboard run build

dev-dashboard:
	npm --prefix dashboard run dev
