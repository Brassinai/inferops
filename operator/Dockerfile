FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/inferops-operator ./operator/cmd/manager

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/inferops-operator /inferops-operator
USER 65532:65532
ENTRYPOINT ["/inferops-operator"]
