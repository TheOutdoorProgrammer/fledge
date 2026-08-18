FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X github.com/nerdswhofish/fledge/internal/version.Version=${VERSION} \
      -X github.com/nerdswhofish/fledge/internal/version.Commit=${COMMIT} \
      -X github.com/nerdswhofish/fledge/internal/version.Date=${DATE}" \
    -o /out/fledged ./cmd/fledged

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/fledged /fledged

# Matches the nonroot user, so a mounted volume can be chowned to it.
USER 65532:65532
VOLUME ["/var/lib/fledge"]
EXPOSE 8080

ENTRYPOINT ["/fledged"]
