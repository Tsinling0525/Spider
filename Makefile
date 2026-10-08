.PHONY: build build-llmd build-agentd build-dashboardd build-dashboard build-xiaozhi-bridge run-xiaozhi-bridge test run run-llmd run-agentd run-dashboardd dev-dashboard

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

build-xiaozhi-bridge:
	go build -o bin/xiaozhi-bridge ./cmd/xiaozhi-bridge

run-xiaozhi-bridge: build-xiaozhi-bridge
	sh scripts/run-xiaozhi-bridge.sh

run-dashboardd: build-dashboardd
	sh scripts/run-dashboardd.sh

build-dashboard:
	npm --prefix dashboard run build

dev-dashboard:
	npm --prefix dashboard run dev
