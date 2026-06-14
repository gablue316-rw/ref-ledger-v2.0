FROM golang:1.25 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o webpage-server ./internal/html

FROM alpine:latest

WORKDIR /root

RUN mkdir -p /root/logs

COPY --from=builder /app/webpage-server .
COPY --from=builder /app/internal/html ./internal/html

EXPOSE 8080

CMD ["./webpage-server"]