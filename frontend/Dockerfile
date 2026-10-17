# syntax=docker/dockerfile:1
# Frontend: build con Node (type-check + Vite) y servido como estático por nginx.
FROM node:24.22.0-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY . .
RUN npm run check && npm run build

FROM nginx:1.31.7-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
# nginx escucha en 8080 (Traefik enruta "/" aquí).
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
