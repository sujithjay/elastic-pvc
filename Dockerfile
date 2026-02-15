FROM golang:1.25 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o elastic-pvc ./cmd/

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /app/elastic-pvc /elastic-pvc
USER nonroot:nonroot
ENTRYPOINT ["/elastic-pvc"]
