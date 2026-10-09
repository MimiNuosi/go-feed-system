FROM golang:1.27-alpine AS build

WORKDIR /src

ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app \
    && mkdir -p /data/videos \
    && chown -R app:app /data

WORKDIR /app

COPY --from=build --chown=app:app /out/api /app/api

ENV HTTP_ADDR=0.0.0.0:8080
ENV LOCAL_STORAGE_DIR=/data/videos

EXPOSE 8080

USER app

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8080/livez >/dev/null || exit 1

ENTRYPOINT ["/app/api"]
