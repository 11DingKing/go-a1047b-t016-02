FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/arctic-express ./cmd/server

FROM golang:1.26 AS verifier
WORKDIR /src
COPY --from=builder /src /src
RUN go test -timeout=120s -count=1 ./...

FROM alpine:3.21
RUN addgroup -S app && adduser -S app -G app
COPY --from=builder /out/arctic-express /usr/local/bin/arctic-express
USER app
EXPOSE 56282
ENTRYPOINT ["arctic-express"]
