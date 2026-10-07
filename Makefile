.PHONY: build build-llmd build-agentd build-dashboardd build-dashboard test run run-llmd run-agentd run-dashboardd dev-dashboard

build:
	go build -o contactd ./cmd/contactd

test:
	go test ./...

run:
	go run ./cmd/contactd

build-llmd:
	go build -o bin/llmd ./cmd/llmd

run-llmd:
	go run ./cmd/llmd

build-agentd:
	go build -o bin/agentd ./cmd/agentd

run-agentd:
	go run ./cmd/agentd

build-dashboardd:
	go build -o bin/dashboardd ./cmd/dashboardd

run-dashboardd:
	go run ./cmd/dashboardd

build-dashboard:
	npm --prefix dashboard run build

dev-dashboard:
	npm --prefix dashboard run dev
