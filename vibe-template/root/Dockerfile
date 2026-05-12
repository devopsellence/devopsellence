FROM golang:1.26-bookworm AS base
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

FROM base AS test
RUN go test ./...

FROM test AS build
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app .
RUN mkdir -p /out/data && touch /out/data/.keep

FROM gcr.io/distroless/static-debian12:nonroot

ENV ADDR=:8080
ENV DB_PATH=/data/app.sqlite
WORKDIR /app
COPY --from=build /out/app /app/app
COPY --from=build --chown=nonroot:nonroot /out/data /data
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/app"]
