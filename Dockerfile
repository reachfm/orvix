FROM node:20-alpine AS frontend-builder

WORKDIR /web
COPY web/package*.json ./
RUN npm install

COPY web/ ./
RUN cd admin && npx vite build --config vite.config.ts --logLevel silent
RUN cd webmail && npx vite build --config vite.config.ts --logLevel silent
RUN cd portal && npx vite build --config vite.config.ts --logLevel silent

FROM golang:1.23-alpine AS backend-builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend-builder /web/admin/dist ./web/admin/dist
COPY --from=frontend-builder /web/webmail/dist ./web/webmail/dist
COPY --from=frontend-builder /web/portal/dist ./web/portal/dist

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=0.1.0 -X main.Product=OrvixEM -X main.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'docker') -X main.Channel=stable" \
    -o /orvix ./cmd/orvix

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=backend-builder /orvix /usr/local/bin/orvix

RUN addgroup -S orvix && adduser -S orvix -G orvix

RUN mkdir -p /etc/orvix /var/lib/orvix/{rollback,snapshots,data} /var/log/orvix
COPY configs/orvix.yaml /etc/orvix/orvix.yaml

RUN chown -R orvix:orvix /etc/orvix /var/lib/orvix /var/log/orvix

USER orvix

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -sf http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/orvix"]
CMD ["start"]
