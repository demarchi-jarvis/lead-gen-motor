# ============================================================
# Lead Gen Motor — Multi-stage Docker Build
# Alvo: imagem final < 50MB (binário Go estático em scratch)
#
# Stage 1 (builder): compila o binário Go com todas as dependências
# Stage 2 (final):   scratch + binário + certificados TLS = ~20-25MB
# ============================================================

# ---- Stage 1: Builder ----
FROM golang:1.22-alpine AS builder

# Dependências de sistema para compilação
# gcc + musl-dev: necessários para cgo (chromedp usa cgo internamente)
RUN apk add --no-cache gcc musl-dev git ca-certificates tzdata

WORKDIR /build

# Copia go.mod e go.sum primeiro (cache de dependências)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copia o código fonte
COPY . .

# Compila o binário
# CGO_ENABLED=0: binário totalmente estático (sem dependências de SO)
# -ldflags: remove informações de debug e reduz tamanho
# -trimpath: remove caminhos absolutos do binário (segurança)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-w -s -X main.versao=1.0.0 -X main.buildTime=$(date -u +%Y%m%d%H%M%S)" \
    -trimpath \
    -o /build/lead-gen-motor \
    ./cmd/server/

# Verifica o tamanho do binário
RUN echo "Tamanho do binário:" && ls -lh /build/lead-gen-motor

# ---- Stage 2: Final (scratch) ----
FROM scratch AS final

# Metadados da imagem
LABEL maintainer="Demarchi MEI <demarchivagas@gmail.com>"
LABEL description="Lead Gen Motor - Omnichannel Prospecting Engine"
LABEL version="1.0.0"

# Certificados TLS — necessários para chamadas HTTPS (SES, APIs, etc.)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Timezone data — para logs com timezone correto
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Binário compilado
COPY --from=builder /build/lead-gen-motor /lead-gen-motor

# Arquivo de limites (configuração de rate limiting)
COPY config/limits.yaml /app/config/limits.yaml

# Porta da aplicação
EXPOSE 8080

# Health check interno
HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
    CMD ["/lead-gen-motor", "-healthcheck"] || exit 1

# Usuário não-root (UID 1000 — best practice de segurança)
# Nota: em scratch não podemos usar USER por nome, apenas UID
USER 1000:1000

ENTRYPOINT ["/lead-gen-motor"]
