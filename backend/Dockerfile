# Общий Dockerfile сервисов. Контекст сборки: каталог backend/ (нужны go.work и общий модуль pkg).
# Пример: docker build -f Dockerfile --build-arg SERVICE=auth .
FROM golang:1.27-alpine AS build
ARG SERVICE
WORKDIR /src
COPY go.work go.work.sum* ./
COPY pkg ./pkg
COPY services ./services
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./services/${SERVICE}/cmd/${SERVICE}

FROM alpine:3.22
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/app /usr/local/bin/app
ENTRYPOINT ["app"]
