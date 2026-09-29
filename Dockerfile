# ---- 构建阶段 ----
FROM golang:1.26 AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /horizon-server .

# ---- 运行阶段 ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /horizon-server /app/horizon-server
RUN mkdir -p /app/downloads /app/data
VOLUME ["/app/downloads", "/app/data"]
EXPOSE 8080
ENTRYPOINT ["/app/horizon-server"]
CMD ["-addr", ":8080"]