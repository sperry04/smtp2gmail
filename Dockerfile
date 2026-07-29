# syntax=docker/dockerfile:1
FROM golang:1.25-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/smtp2gmail ./cmd/smtp2gmail

# distroless/static bundles CA certificates (needed for TLS to
# googleapis.com) with no shell or package manager -- see README "Base
# image" for why this was chosen over scratch/alpine.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/smtp2gmail /smtp2gmail
ENTRYPOINT ["/smtp2gmail"]
