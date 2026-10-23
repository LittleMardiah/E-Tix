#!/bin/bash
echo "🚀 Memulai E-Tix Flash Deployment (Self-Hosted)..."

cd backend

if [ -f .env.local ]; then
    set -a
    source .env.local
    set +a
    echo "✅ .env.local loaded."
else
    echo "❌ .env.local NOT FOUND. Aborting."
    exit 1
fi

if [ -z "${JWT_PRIVATE_KEY:-}" ] && [ -f "${JWT_PRIVATE_KEY_PATH:-}" ]; then
    export JWT_PRIVATE_KEY="$(cat "$JWT_PRIVATE_KEY_PATH")"
fi
if [ -z "${JWT_PUBLIC_KEY:-}" ] && [ -f "${JWT_PUBLIC_KEY_PATH:-}" ]; then
    export JWT_PUBLIC_KEY="$(cat "$JWT_PUBLIC_KEY_PATH")"
fi

echo "⏳ Menjalankan Backend (Port 8080)..."
go run ./cmd/api/main.go &
BACKEND_PID=$!
cd ..

sleep 3

echo ""
echo "🌐 Membuka Ngrok Tunnel (URL Permanen)..."
echo "✅ URL: https://sari-inartificial-dwain.ngrok-free.dev"
echo "--------------------------------------------------------"
ngrok http --domain=sari-inartificial-dwain.ngrok-free.dev 8080

trap "echo '🛑 Mematikan Backend...'; kill $BACKEND_PID 2>/dev/null" EXIT
