FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/spider-mail ./cmd/spider-mail

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/spider-mail /spider-mail
VOLUME ["/data"]
ENV SPIDER_DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/spider-mail"]
