# Multi-stage build for agent-boss coordinator
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /boss ./cmd/boss/

# Final minimal image
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /boss /boss

EXPOSE 8899

ENTRYPOINT ["/boss", "serve"]
