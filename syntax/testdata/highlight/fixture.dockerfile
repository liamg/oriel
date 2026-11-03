# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN go build -o /oriel ./cmd/oriel

FROM gcr.io/distroless/base
COPY --from=build /oriel /oriel
ENTRYPOINT ["/oriel"]
