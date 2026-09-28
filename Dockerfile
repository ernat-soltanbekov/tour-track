FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /app /tour-track
USER 65532:65532
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
ENTRYPOINT ["/tour-track"]
