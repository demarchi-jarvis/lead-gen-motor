# Docker — Lead Gen Motor

## Arquitetura de Containers

```
Docker Network: lead-gen-net (bridge, 172.20.0.0/24)
│
├── postgres (172.20.0.x)
│   Image: postgres:16-alpine
│   Port: 127.0.0.1:5432:5432 (somente localhost)
│   Volume: /opt/lead-gen-motor/data/postgres
│
├── chromedp (172.20.0.x)
│   Image: chromedp/headless-shell:latest
│   Port: 127.0.0.1:9222:9222 (somente localhost)
│   CDP: ws://chromedp:9222
│
└── app (172.20.0.x)
    Image: lead-gen-motor:latest (scratch ~20MB)
    Port: 0.0.0.0:8080:8080 (exposto para internet)
    Deps: postgres (healthy) + chromedp (healthy)
```

**Segurança de portas:**
- PostgreSQL e Chrome ficam acessíveis **apenas internamente** (`127.0.0.1`)
- Apenas a porta 8080 da aplicação Go é exposta publicamente

---

## Dockerfile Multi-Stage — Por Que Dois Stages?

### Stage 1: `builder` (golang:1.22-alpine)

Propósito: compilar o binário Go com todas as ferramentas de build.

```dockerfile
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev git ca-certificates tzdata

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download          # cache das dependências em camada separada

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-w -s" \       # remove debug symbols (-w) e tabela de símbolos (-s)
    -trimpath \              # remove caminhos absolutos do host do binário
    -o /build/lead-gen-motor ./cmd/server/
```

**Por que `CGO_ENABLED=0`?**  
Produz um binário **completamente estático** — sem dependências de bibliotecas do sistema operacional. Isso permite rodar em `scratch` (imagem vazia).

**Por que `-ldflags="-w -s"`?**  
- `-w`: remove DWARF debug info (~20-30% menor)
- `-s`: remove symbol table (~10% menor)
- Resultado: binário ~15-20MB vs ~25-30MB sem as flags

### Stage 2: `final` (scratch)

Propósito: imagem de produção mínima e segura.

```dockerfile
FROM scratch AS final

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /build/lead-gen-motor /lead-gen-motor
COPY config/limits.yaml /app/config/limits.yaml

USER 1000:1000
ENTRYPOINT ["/lead-gen-motor"]
```

**Por que `scratch`?**
- Zero camadas extras, zero ferramentas de sistema
- Nenhum shell disponível → reduz superfície de ataque
- Imagem final típica: **18-25MB**

**Por que copiar `ca-certificates.crt`?**  
O binário Go precisa verificar certificados TLS ao fazer chamadas HTTPS (SES, Meta API, etc.). Sem isso, todas as chamadas HTTPS falhariam.

**Por que copiar `zoneinfo`?**  
Para que `time.LoadLocation("America/Sao_Paulo")` funcione corretamente dentro do container.

---

## Docker Compose — Serviços em Detalhe

### PostgreSQL

```yaml
postgres:
  image: postgres:16-alpine
  command: >
    postgres
      -c max_connections=50
      -c shared_buffers=128MB
      -c log_min_duration_statement=1000
```

**Tuning aplicado:**
- `max_connections=50`: t3.medium tem 4GB RAM, 50 conexões é suficiente para o pool Go (10 conns)
- `shared_buffers=128MB`: 25% da RAM disponível para o PG é adequado
- `log_min_duration_statement=1000`: loga queries acima de 1s para diagnóstico

**Volume persistente:**
```yaml
volumes:
  - type: bind
    source: /opt/lead-gen-motor/data/postgres
    target: /var/lib/postgresql/data
```
Bind mount em vez de volume nomeado para facilitar backup direto no host.

**Migrations automáticas:**
```yaml
volumes:
  - ../migrations:/docker-entrypoint-initdb.d:ro
```
O PostgreSQL executa automaticamente qualquer `.sql` em `/docker-entrypoint-initdb.d/` na **primeira inicialização** (quando o volume está vazio).

### Chrome Headless

```yaml
chromedp:
  image: chromedp/headless-shell:latest
  ports:
    - "127.0.0.1:9222:9222"
  cap_add:
    - SYS_ADMIN    # necessário para o sandbox do Chrome funcionar
```

**Conexão via CDP WebSocket:**
O adapter Go se conecta como: `ws://chromedp:9222`

O chromedp/headless-shell é uma imagem otimizada do Chrome headless mantida pelo time do chromedp. É menor e mais estável que a imagem completa do Chrome.

### Aplicação Go

```yaml
app:
  depends_on:
    postgres:
      condition: service_healthy    # aguarda pg_isready passar
    chromedp:
      condition: service_healthy    # aguarda Chrome responder no 9222
```

**Recursos alocados:**
```yaml
deploy:
  resources:
    limits:
      memory: 256M    # Go é muito eficiente em memória
      cpus: "1.0"
```

Com 5 workers, o Go usa tipicamente 50-100MB de RAM para este workload.

---

## Comandos Docker Essenciais

### Desenvolvimento Local

```bash
# Subir tudo em background
docker-compose up -d

# Subir com build forçado (após alterar código)
docker-compose up -d --build app

# Acompanhar logs em tempo real
docker-compose logs -f app
docker-compose logs -f app postgres  # múltiplos serviços

# Ver status dos containers
docker-compose ps

# Parar tudo (preserva volumes)
docker-compose down

# Parar e apagar volumes (LIMPA O BANCO)
docker-compose down -v
```

### Inspecção

```bash
# Entrar no container do banco (para queries manuais)
docker-compose exec postgres psql -U leadgen leadgen

# Ver processos dentro do container
docker-compose exec app /bin/sh  # ERRO: scratch não tem shell
# Use: docker inspect lead-gen-app

# Ver uso de recursos
docker stats

# Verificar tamanho da imagem
docker images lead-gen-motor
```

### Banco de Dados

```bash
# Backup manual
docker-compose exec -T postgres \
  pg_dump -U leadgen leadgen | gzip > backup_$(date +%Y%m%d).sql.gz

# Restaurar backup
gunzip -c backup_YYYYMMDD.sql.gz | \
  docker-compose exec -T postgres psql -U leadgen leadgen

# Query rápida
docker-compose exec postgres \
  psql -U leadgen leadgen -c "SELECT COUNT(*), status FROM leads GROUP BY status;"

# Ver jobs pendentes
docker-compose exec postgres \
  psql -U leadgen leadgen -c "SELECT * FROM jobs WHERE status='pendente' LIMIT 10;"
```

---

## Build para Produção

### Build Local + Push para EC2

```bash
# Constrói a imagem localmente
docker build -t lead-gen-motor:latest .

# Verifica tamanho
docker images lead-gen-motor:latest

# Exporta como arquivo
docker save lead-gen-motor:latest | gzip > lead-gen-motor.tar.gz

# Copia para EC2
scp -i terraform/lead-gen-key lead-gen-motor.tar.gz ec2-user@$IP:/opt/lead-gen-motor/

# Importa na EC2
ssh -i terraform/lead-gen-key ec2-user@$IP \
  "docker load < /opt/lead-gen-motor/lead-gen-motor.tar.gz"
```

### Usando ECR (Opcional — AWS Academy pode não ter permissão)

```bash
# Autenticar no ECR
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin CONTA.dkr.ecr.us-east-1.amazonaws.com

# Tag e push
docker tag lead-gen-motor:latest CONTA.dkr.ecr.us-east-1.amazonaws.com/lead-gen-motor:latest
docker push CONTA.dkr.ecr.us-east-1.amazonaws.com/lead-gen-motor:latest
```

---

## Profiles do Docker Compose

```bash
# Desenvolvimento (inclui pgAdmin na :5050)
docker-compose --profile dev up -d

# Produção (sem pgAdmin)
docker-compose up -d
```

O pgAdmin só sobe com o profile `dev` explícito — não consome recursos em produção.

---

## Healthchecks

Todos os serviços têm healthcheck configurado:

| Serviço | Comando | Intervalo | Timeout |
|---|---|---|---|
| postgres | `pg_isready -U leadgen` | 10s | 5s |
| chromedp | `wget -q --spider http://localhost:9222/json/version` | 30s | 10s |
| app | `wget -q --spider http://localhost:8080/api/v1/health` | 30s | 10s |

O `depends_on: condition: service_healthy` garante que a aplicação Go só inicia **após** o banco e o Chrome estarem prontos.
