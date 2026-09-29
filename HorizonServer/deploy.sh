#!/usr/bin/env bash
# 地平线磁力下载 · 云端服务 一键部署脚本（Linux）
# 用法：将整个部署包上传到服务器后，cd 到该目录执行  bash deploy.sh
set -euo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-8080}"
gen_secret() { openssl rand -hex 32 2>/dev/null || od -An -N16 -tx1 /dev/urandom | tr -d ' \n'; }

# 优先全栈（PostgreSQL + Redis + MinIO + 监控）；服务器无 Docker 则直接跑二进制（SQLite）
if command -v docker >/dev/null 2>&1 && [ -f docker-compose.yml ]; then
  echo "[deploy] 检测到 Docker，使用全栈编排（PostgreSQL + Redis + MinIO + Prometheus + Grafana）"
  if [ ! -f .env ]; then
    {
      echo "HORIZON_JWT_SECRET=$(gen_secret)"
      echo "POSTGRES_PASSWORD=$(gen_secret | cut -c1-24)"
      echo "MINIO_ROOT_PASSWORD=$(gen_secret | cut -c1-24)"
      echo "GRAFANA_PASSWORD=$(gen_secret | cut -c1-24)"
      # 电影天堂影视元数据（封面/名称/简介）：默认开启，客户端「影视库」依赖它
      echo "HORIZON_DYTT_ENABLED=true"
      # 中文片名自动翻译成英文再查英文索引站（否则中文搜索返回满屏无关结果）
      echo "HORIZON_TRANSLATE_ENABLED=true"
      echo "HORIZON_TPB_BASE=https://apibay.org"
      # DHT 走 UDP，网络封锁 BT 的 UDP 时设 false
      echo "HORIZON_DHT_ENABLED=true"`n      # uTP 走 UDP，默认关闭强制 TCP
    } > .env
    echo "[deploy] 已生成 .env（含强随机密钥）"
  fi
  docker compose up -d --build
  echo "[deploy] 完成。健康检查: http://<服务器IP>:8080/healthz"
  echo "[deploy] 管理后台: Grafana http://<服务器IP>:3000 / MinIO控制台 http://<服务器IP>:9001"
  exit 0
fi

echo "[deploy] 未检测到 Docker，直接运行 Linux 二进制（SQLite 存储，零依赖）"
mkdir -p downloads data
chmod +x horizon-server-linux 2>/dev/null || true
SECRET=$(gen_secret)
nohup ./horizon-server-linux -jwt-secret "$SECRET" -addr ":$PORT" -dir ./downloads > server.log 2>&1 &
PID=$!
echo "[deploy] 进程 PID=$PID"
echo "[deploy] jwt-secret=$SECRET" > server.secret
sleep 2
if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
  echo "[deploy] 健康检查通过 ✓"
else
  echo "[deploy] 健康检查未通过，请查看 server.log"
fi
echo "[deploy] 完成。健康检查: http://<服务器IP>:$PORT/healthz"
echo "[deploy] 提示：重启后进程会退出，生产环境建议用 systemd（见 部署说明.md）"