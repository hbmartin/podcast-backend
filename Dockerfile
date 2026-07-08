FROM golang:1.25.12-alpine3.24 AS builder
WORKDIR /app

RUN apk update && apk add --no-cache ca-certificates && update-ca-certificates

ENV USER=appuser
ENV UID=10001 
RUN adduser \    
    --disabled-password \    
    --gecos "" \    
    --home "/nonexistent" \    
    --shell "/sbin/nologin" \    
    --no-create-home \    
    --uid "${UID}" \    
    "${USER}"
    
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w" -o ./podcast-backend ./main.go

FROM scratch AS runner
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
WORKDIR /app
COPY --from=builder /app/podcast-backend .
COPY --from=builder /app/db/migrations/ ./db/migrations/
USER appuser:appuser
EXPOSE 8000
ENTRYPOINT ["./podcast-backend"]
