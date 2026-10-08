FROM node:22-bookworm-slim AS frontend
WORKDIR /src/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci
COPY dashboard ./
RUN npm run build

FROM golang:1.24-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dashboardd ./cmd/dashboardd

FROM python:3.13-slim-bookworm
RUN groupadd --gid 10001 spider && useradd --uid 10001 --gid spider --no-create-home spider \
    && mkdir -p /app /data && chown spider:spider /data
WORKDIR /app
COPY --from=builder /out/dashboardd /app/dashboardd
COPY --from=frontend /src/dashboard/dist /app/dashboard/dist
ENV SPIDER_DATA_DIR=/data \
    SPIDER_DASHBOARD_DIST=/app/dashboard/dist \
    SPIDER_DASHBOARD_PYTHON=python3 \
    SPIDER_DASHBOARD_LISTEN_ADDRESS=0.0.0.0:8083
USER spider
VOLUME ["/data"]
EXPOSE 8083
ENTRYPOINT ["/app/dashboardd"]
