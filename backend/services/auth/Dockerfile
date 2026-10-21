# Контекст сборки: каталог backend/ (нужен go.work и общий модуль pkg).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY pkg ./pkg
COPY services ./services
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/auth ./services/auth/cmd/auth

FROM alpine:3.22
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/auth /usr/local/bin/auth
EXPOSE 8081
ENTRYPOINT ["auth"]
