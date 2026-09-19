FROM golang:1.24-alpine AS build
WORKDIR /src
# 服务通过 cgo 绑定系统 libsqlite3，构建阶段需要 C 工具链与开发头文件
RUN apk add --no-cache build-base sqlite-dev
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /out/server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache sqlite-libs
WORKDIR /app
COPY --from=build /out/server /app/server
ENV PORT=8080 DATABASE_PATH=/data/app.sqlite3
EXPOSE 8080
# 服务启动时自动应用 migrations 中的迁移脚本
CMD ["/bin/sh", "-c", "mkdir -p /data && exec /app/server"]
