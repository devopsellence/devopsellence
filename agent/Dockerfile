FROM golang:1.25 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/devopsellence ./cmd/devopsellence

FROM gcr.io/distroless/static
COPY --from=build /out/devopsellence /usr/local/bin/devopsellence
USER 0:0
ENTRYPOINT ["/usr/local/bin/devopsellence"]
