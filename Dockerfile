FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/likespotifybot .

FROM alpine:3.20

WORKDIR /app
RUN apk add --no-cache ca-certificates

COPY --from=builder /out/likespotifybot /app/likespotifybot
COPY --from=builder /src/assets /app/assets

EXPOSE 8080
CMD ["/app/likespotifybot"]
