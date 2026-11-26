# Single image: Svelte build + static Go binary. Used by docker-compose.yml (VPS alternative to Sprites).

FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.27-alpine AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/photo-share .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 app && mkdir -p /app/data && chown app /app/data
WORKDIR /app
COPY --from=backend /out/photo-share /app/photo-share
COPY --from=frontend /src/frontend/dist /app/frontend/dist
ENV PORT=8080 STATIC_DIR=/app/frontend/dist SQLITE_PATH=/app/data/photoshare.db PHOTO_DIR=/app/data/photos
USER app
EXPOSE 8080
CMD ["/app/photo-share"]
