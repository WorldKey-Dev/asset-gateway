# asset-gateway —— 资产分发只读热路径（Go；与 cloud-console 分离镜像线）
FROM golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/asset-gateway .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/asset-gateway /asset-gateway
EXPOSE 8095
USER nonroot:nonroot
ENTRYPOINT ["/asset-gateway"]
