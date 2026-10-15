# syntax=docker/dockerfile:1
# Se construye con el contexto en la raíz del repositorio (necesita api/ y pkg/):
#   docker build -f services/orders/Dockerfile .
FROM golang:1.27-alpine AS build
WORKDIR /src
ARG VERSION=dev
COPY go.mod go.sum ./
COPY api ./api
COPY pkg ./pkg
COPY services/orders ./services/orders
# Las cachés de BuildKit evitan descargar módulos y recompilar en cada build.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" \
      -o /out/orders ./services/orders/cmd/orders

# Imagen final sin shell, ejecutada como usuario no root.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/orders /app/orders
COPY services/orders/configs /app/configs
EXPOSE 8000 9000
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
  CMD ["/app/orders", "-healthcheck", "http://127.0.0.1:8000/livez"]
ENTRYPOINT ["/app/orders"]
CMD ["-conf", "/app/configs/config.yaml"]
