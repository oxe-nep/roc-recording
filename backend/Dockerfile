FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o roc-recording ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ffmpeg
WORKDIR /app
COPY --from=builder /app/roc-recording .
COPY config.yaml .
RUN mkdir -p hls
EXPOSE 8080
ENTRYPOINT ["./roc-recording"]
