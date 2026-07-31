FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/inferops-cloud-agent ./cloud-agent/cmd/cloud-agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/inferops-cloud-agent /inferops-cloud-agent
USER 65532:65532
ENTRYPOINT ["/inferops-cloud-agent"]
