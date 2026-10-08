.PHONY: build build-dashboardd build-dashboard build-xiaozhi-bridge run-xiaozhi-bridge test run run-dashboardd dev-dashboard

build: build-dashboard build-dashboardd

test:
	go test ./...

run: run-dashboardd

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
