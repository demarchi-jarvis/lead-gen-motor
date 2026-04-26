#!/bin/bash
# ============================================================
# start.sh — Lead Gen Motor: inicialização em um clique
#
# O QUE FAZ:
#   1. Verifica pré-requisitos (Docker, docker-compose)
#   2. Cria .env com valores padrão se não existir
#   3. Gera go.sum via container golang (se não existir)
#   4. Cria diretórios necessários
#   5. Sobe PostgreSQL + Chrome + App via docker-compose
#   6. Aguarda health check e exibe status
#
# USO:
#   chmod +x start.sh
#   ./start.sh              # inicialização padrão
#   ./start.sh --rebuild    # força rebuild da imagem Go
#   ./start.sh --stop       # para todos os containers
#   ./start.sh --logs       # mostra logs em tempo real
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_DIR="$SCRIPT_DIR/app"

# Cores
VERDE='\033[0;32m'
AMARELO='\033[1;33m'
VERMELHO='\033[0;31m'
AZUL='\033[0;34m'
CINZA='\033[0;37m'
RESET='\033[0m'

ok()   { echo -e "${VERDE}  ✓${RESET} $*"; }
info() { echo -e "${AZUL}  →${RESET} $*"; }
warn() { echo -e "${AMARELO}  !${RESET} $*"; }
erro() { echo -e "${VERMELHO}  ✗${RESET} $*" >&2; }
sep()  { echo -e "${CINZA}────────────────────────────────────────────${RESET}"; }

# ============================================================
# Subcomandos
# ============================================================
case "${1:-start}" in
  --stop|stop)
    info "Parando containers..."
    cd "$APP_DIR" && docker compose down --timeout 15
    ok "Containers parados."
    exit 0
    ;;
  --logs|logs)
    cd "$APP_DIR" && docker compose logs -f app
    exit 0
    ;;
  --status|status)
    cd "$APP_DIR" && docker compose ps
    echo ""
    curl -sf http://localhost:8080/api/v1/status | python3 -m json.tool 2>/dev/null || \
      echo "Aplicação não respondendo em :8080"
    exit 0
    ;;
  --rebuild|rebuild|start|"")
    # continua abaixo
    ;;
  *)
    echo "Uso: $0 [--rebuild|--stop|--logs|--status]"
    exit 1
    ;;
esac

REBUILD=""
if [[ "${1:-}" == "--rebuild" || "${1:-}" == "rebuild" ]]; then
  REBUILD="--build"
fi

echo ""
echo -e "${AZUL}============================================================${RESET}"
echo -e "${AZUL}   Lead Gen Motor — Inicialização${RESET}"
echo -e "${AZUL}============================================================${RESET}"
echo ""

# ============================================================
# 1. Pré-requisitos
# ============================================================
sep
info "Verificando pré-requisitos..."

FALTANDO=0
for cmd in docker; do
  if ! command -v "$cmd" &>/dev/null; then
    erro "Comando '$cmd' não encontrado."
    FALTANDO=1
  fi
done

if [[ $FALTANDO -eq 1 ]]; then
  erro "Instale o Docker: https://docs.docker.com/engine/install/"
  exit 1
fi

if ! docker info &>/dev/null; then
  erro "Docker daemon não está rodando. Inicie o Docker e tente novamente."
  exit 1
fi

# docker compose (v2) ou docker-compose (v1)
if docker compose version &>/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose &>/dev/null; then
  COMPOSE="docker-compose"
else
  erro "docker compose não encontrado. Instale Docker Compose v2."
  exit 1
fi

ok "Docker $(docker --version | awk '{print $3}' | tr -d ',')"
ok "Compose: $($COMPOSE version --short 2>/dev/null || echo 'v1')"

# ============================================================
# 2. Arquivo .env
# ============================================================
sep
ENV_FILE="$APP_DIR/.env"

if [[ ! -f "$ENV_FILE" ]]; then
  warn ".env não encontrado. Criando a partir do template..."
  cp "$APP_DIR/.env.example" "$ENV_FILE" 2>/dev/null || true

  # Garante que DB_SENHA tem valor (obrigatório para o app iniciar)
  if ! grep -q "^DB_SENHA=" "$ENV_FILE" || grep -q "^DB_SENHA=$" "$ENV_FILE"; then
    echo "DB_SENHA=Leadgen@2024!Forte" >> "$ENV_FILE"
  fi

  warn "Arquivo .env criado em: $ENV_FILE"
  warn "EDITE as chaves antes de usar em produção:"
  warn "  - SES_REMETENTE_EMAIL  (email verificado no AWS SES)"
  warn "  - WHATSAPP_TOKEN       (token Meta Business API)"
  warn "  - WHATSAPP_PHONE_ID    (Phone Number ID Meta)"
  warn "  - SNS_NUMERO_CLOSER    (seu número em formato +55...)"
  echo ""
else
  ok ".env encontrado: $ENV_FILE"
fi

# Verifica se DB_SENHA está definida (app não inicia sem ela)
DB_SENHA_VAL=$(grep "^DB_SENHA=" "$ENV_FILE" | cut -d= -f2- | tr -d '"')
if [[ -z "$DB_SENHA_VAL" ]]; then
  erro "DB_SENHA está vazia no .env. Defina uma senha para o PostgreSQL."
  exit 1
fi

# ============================================================
# 3. go.sum (gerado via container golang se não existir)
# ============================================================
sep
GOSUM="$APP_DIR/go.sum"

if [[ ! -f "$GOSUM" ]]; then
  info "go.sum não encontrado. Gerando via container golang:1.22-alpine..."
  info "(primeiro download pode demorar ~1 minuto)"

  docker run --rm \
    -v "$APP_DIR:/app" \
    -w /app \
    -e GOMODCACHE=/tmp/gomodcache \
    -e GOPATH=/tmp/gopath \
    golang:1.22-alpine \
    sh -c "apk add --no-cache git >/dev/null 2>&1 && go mod tidy 2>&1"

  if [[ -f "$GOSUM" ]]; then
    ok "go.sum gerado com sucesso"
  else
    erro "Falha ao gerar go.sum. Verifique a conexão com a internet."
    exit 1
  fi
else
  ok "go.sum já existe"
fi

# ============================================================
# 4. Atualiza Dockerfile para usar go.sum (agora existe)
# ============================================================
# Garante que o Dockerfile copia go.sum quando ele existe
if grep -q "^COPY go.mod ./\$" "$APP_DIR/Dockerfile"; then
  sed -i 's/^COPY go.mod \.\/$/COPY go.mod go.sum .\//' "$APP_DIR/Dockerfile"
  info "Dockerfile atualizado para incluir go.sum"
fi

# ============================================================
# 5. Diretórios necessários
# ============================================================
sep
info "Criando diretórios necessários..."
mkdir -p /var/log/lead-gen 2>/dev/null || true
ok "Diretórios prontos"

# ============================================================
# 6. Sobe os containers
# ============================================================
sep
info "Iniciando containers (postgres + chromedp + app)..."
echo ""

cd "$APP_DIR"

# Para containers antigos se existirem (evita conflito)
$COMPOSE down --timeout 5 2>/dev/null || true

# Sobe tudo (--build força rebuild se --rebuild foi passado)
$COMPOSE up -d $REBUILD

echo ""
info "Aguardando PostgreSQL ficar pronto..."
TENTATIVA=0
until $COMPOSE exec -T postgres pg_isready -U "${DB_USUARIO:-leadgen}" -d "${DB_NOME:-leadgen}" &>/dev/null; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge 30 ]]; then
    erro "Timeout aguardando PostgreSQL."
    $COMPOSE logs --tail=20 postgres
    exit 1
  fi
  sleep 2
  echo -n "."
done
echo ""
ok "PostgreSQL pronto"

info "Aguardando aplicação Go ficar pronta..."
TENTATIVA=0
until curl -sf http://localhost:8080/api/v1/health &>/dev/null; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge 30 ]]; then
    warn "Aplicação não respondeu ao health check. Verifique os logs:"
    $COMPOSE logs --tail=30 app
    break
  fi
  sleep 3
  echo -n "."
done
echo ""

# ============================================================
# 7. Status final
# ============================================================
sep
echo ""
echo -e "${VERDE}============================================================${RESET}"
echo -e "${VERDE}   Lead Gen Motor — RODANDO!${RESET}"
echo -e "${VERDE}============================================================${RESET}"
echo ""

$COMPOSE ps

echo ""
echo -e "  ${AZUL}API:${RESET}      http://localhost:8080/api/v1"
echo -e "  ${AZUL}Health:${RESET}   http://localhost:8080/api/v1/health"
echo -e "  ${AZUL}Status:${RESET}   http://localhost:8080/api/v1/status"
echo ""
echo -e "  ${CINZA}Logs:${RESET}     ./start.sh --logs"
echo -e "  ${CINZA}Parar:${RESET}    ./start.sh --stop"
echo -e "  ${CINZA}Rebuild:${RESET}  ./start.sh --rebuild"
echo ""

# Testa o health check e mostra resposta
HEALTH=$(curl -sf http://localhost:8080/api/v1/health 2>/dev/null || echo '{"status":"indisponível"}')
echo -e "  Health check: ${VERDE}$HEALTH${RESET}"
echo ""
