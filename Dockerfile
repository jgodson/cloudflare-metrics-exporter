FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/cloudflare-metrics-exporter ./cmd/exporter

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/cloudflare-metrics-exporter /cloudflare-metrics-exporter

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/cloudflare-metrics-exporter"]
