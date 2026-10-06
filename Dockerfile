FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/loyaltyledger ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -o /out/orderstub ./cmd/orderstub

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/ /app/
COPY config.yaml /app/config.yaml
COPY testdata/orders.json /app/testdata/orders.json
USER app
EXPOSE 8080
CMD ["/app/loyaltyledger"]
