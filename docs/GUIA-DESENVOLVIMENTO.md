# Guia de Desenvolvimento — Lead Gen Motor

> Para qualquer IA ou desenvolvedor que vá incrementar este projeto.
> Leia este documento antes de modificar qualquer arquivo.

---

## Setup Local em 5 Minutos

```bash
# Pré-requisitos: Go 1.22+, Docker, Docker Compose

# 1. Clone e configure
git clone <repo>
cd lead-gen-motor/app

# 2. Configure variáveis mínimas para desenvolvimento
cp .env.example .env
# Edite .env: defina DB_SENHA (ex: dev_senha_123)

# 3. Suba banco + Chrome
docker-compose up -d postgres chromedp

# 4. Compile e rode localmente
go run ./cmd/server/

# 5. Teste
curl http://localhost:8080/api/v1/health
```

Para subir a aplicação via Docker também:
```bash
docker-compose up -d  # sobe tudo incluindo o app
```

---

## Filosofia de Desenvolvimento

### Princípio 1: O Domínio Não Conhece o Mundo

Ao adicionar lógica nova, pergunte-se: "Isso é uma regra de negócio (domínio) ou uma implementação técnica (adapter)?"

- **Regra de negócio** (ex: "lead com score > 60 é quente") → vai em `pkg/domain/` ou `pkg/core/`
- **Implementação técnica** (ex: "enviar email via SES") → vai em `pkg/adapters/`

### Princípio 2: Tudo é Configurável

Números mágicos não existem no código. Qualquer threshold, limite ou intervalo que possa mudar vai em `config/limits.yaml` ou em variável de ambiente.

### Princípio 3: Erros São Cidadãos de Primeira Classe

```go
// ERRADO — erro silencioso
result, _ := someOperation()

// ERRADO — erro genérico sem contexto
return err

// CERTO — erro envolvido com contexto
result, err := someOperation()
if err != nil {
    return nil, fmt.Errorf("scoring: falha ao calcular pontos para lead '%s': %w", leadID, err)
}
```

### Princípio 4: Goroutines Respeitam Context

```go
// ERRADO — goroutine que ignora shutdown
go func() {
    for {
        doWork()
        time.Sleep(30 * time.Second)
    }
}()

// CERTO — goroutine que respeita cancelamento
go func() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return  // shutdown limpo
        case <-ticker.C:
            doWork()
        }
    }
}()
```

---

## Adicionando Features — Checklists

### ✅ Checklist: Novo Canal de Mensagem

- [ ] Constante `Canal` em `pkg/domain/lead.go`
- [ ] Campo de contato no `Lead` se necessário (ex: `TelegramUsername`)
- [ ] Arquivo `pkg/adapters/{canal}/adapter.go` implementando `MessengerPort`
- [ ] Config em `pkg/core/worker.go` → `LimitesDefault`
- [ ] Entrada em `config/limits.yaml`
- [ ] Instanciação no `cmd/server/main.go`
- [ ] Enum `canal_tipo` na próxima migration SQL
- [ ] Documentação em `docs/BACKEND.md` (seção Adapters)
- [ ] Regras de rate limit em `docs/REGRAS-NEGOCIO.md`

### ✅ Checklist: Nova Regra de Negócio

- [ ] Documentar em `docs/REGRAS-NEGOCIO.md` ANTES de implementar
- [ ] Implementar na camada correta (domain ou core, nunca adapter)
- [ ] Configurar parâmetros em `limits.yaml` se aplicável
- [ ] Atualizar `config/config.go` se adicionou novo campo de config
- [ ] Escrever teste unitário em `_test.go`

### ✅ Checklist: Novo Endpoint HTTP

- [ ] Handler em `cmd/server/main.go` (ou `pkg/api/` quando o arquivo crescer)
- [ ] Rota em `registrarRotas()`
- [ ] Documentar em `docs/API.md` com exemplo de request/response
- [ ] Validação de input no handler (nunca confiar no client)
- [ ] Erro adequado com HTTP status correto

### ✅ Checklist: Novo Recurso Terraform

- [ ] Arquivo `.tf` correto por categoria (networking/compute/iam)
- [ ] Variável em `variables.tf` com descrição e default
- [ ] Output útil em `outputs.tf`
- [ ] Estimativa de custo em comentário
- [ ] Tags obrigatórias (Projeto, Ambiente, Gerenciado)
- [ ] Atualizar `docs/INFRAESTRUTURA.md`

### ✅ Checklist: Migration de Banco

- [ ] Arquivo numerado: `migrations/002_descricao.sql`
- [ ] Idempotente: usar `IF NOT EXISTS`, `IF EXISTS`, `ON CONFLICT DO NOTHING`
- [ ] Sem dados destrutivos sem backup
- [ ] Atualizar mapa de schema em `docs/BANCO-DE-DADOS.md`

---

## Padrões de Código Go Deste Projeto

### Nomeação

```go
// Funções exportadas: PascalCase em inglês
func NovoDispatcher(...) *Dispatcher {}

// Variáveis internas: camelCase em inglês
var taxaPorSegundo rate.Limit

// Logs e mensagens de erro: português
logger.Info("dispatcher: ciclo de despacho", ...)
return fmt.Errorf("scoring: lead não encontrado: %w", err)
```

### Estrutura de Adapter

Todo adapter segue este padrão:

```go
package canal

// ConfigAdapter contém credenciais e configurações
type ConfigAdapter struct {
    Campo1 string
    Campo2 string
}

// AdaptadorNome implementa ports.MessengerPort
type AdaptadorNome struct {
    cfg    ConfigAdapter
    logger *slog.Logger
    // ... cliente HTTP, SDK, etc
}

// Novo valida a configuração e instancia o adapter
// Retorna erro se configuração inválida (nunca nil silencioso)
func Novo(cfg ConfigAdapter, logger *slog.Logger) (*AdaptadorNome, error) {
    if cfg.Campo1 == "" {
        return nil, fmt.Errorf("canal: Campo1 é obrigatório")
    }
    return &AdaptadorNome{cfg: cfg, logger: logger}, nil
}

// Compile-time interface check
var _ ports.MessengerPort = (*AdaptadorNome)(nil)

func (a *AdaptadorNome) Canal() domain.Canal    { return domain.CanalNome }
func (a *AdaptadorNome) Disponivel() bool       { return a.cfg.Campo1 != "" }
func (a *AdaptadorNome) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
    // implementação
}
```

### Injeção de Dependência

Este projeto usa **DI manual** (sem container). Toda a composição acontece em `cmd/server/main.go`:

```go
// Ordem correta de instanciação:
// 1. Infraestrutura (banco, AWS)
// 2. Repositórios (usam banco)
// 3. Adapters (usam AWS e outros externos)
// 4. Core (usa repositórios e adapters via interfaces)
// 5. HTTP Handler (usa core)
// 6. Servidor HTTP
```

---

## Testando

### Testes Unitários

```bash
# Roda todos os testes
go test ./...

# Testes com verbose e cobertura
go test -v -cover ./pkg/...

# Teste de um pacote específico
go test -v ./pkg/core/...

# Teste de um caso específico
go test -run TestLeadScoring ./pkg/core/
```

### Estrutura de Testes

```
pkg/
├── domain/
│   ├── lead.go
│   └── lead_test.go         ← testa regras de negócio puras
├── core/
│   ├── scoring.go
│   └── scoring_test.go      ← testa scoring com mock de LeadRepository
└── adapters/
    └── email/
        ├── ses.go
        └── ses_test.go      ← testa parsing de erros SES, normalização
```

### Mock de Repositório para Testes

```go
// pkg/core/scoring_test.go
type mockLeadRepo struct {
    leads       map[string]*domain.Lead
    interacoes  map[string]int
}

func (m *mockLeadRepo) BuscarPorID(ctx context.Context, id string) (*domain.Lead, error) {
    if l, ok := m.leads[id]; ok {
        return l, nil
    }
    return nil, fmt.Errorf("lead não encontrado")
}
// ... implementar os outros métodos da interface
```

---

## Debugging

### Ver o que está na fila de jobs

```bash
docker-compose exec postgres psql -U leadgen leadgen -c "
  SELECT j.id, l.nome, j.canal, j.status, j.tentativas, j.criado_em
  FROM jobs j
  JOIN leads l ON l.id = j.lead_id
  WHERE j.status IN ('pendente', 'processando')
  ORDER BY j.criado_em DESC
  LIMIT 20;
"
```

### Ver logs estruturados

```bash
# Filtra apenas erros
docker-compose logs app | grep '"level":"ERROR"'

# Filtra por lead específico
docker-compose logs app | grep '"lead_id":"UUID_AQUI"'

# Ver score de um lead
docker-compose exec postgres psql -U leadgen leadgen -c "
  SELECT nome, status, pontuacao, ultimo_contato_em
  FROM leads WHERE id='UUID_AQUI';
"
```

### Inspecionar rate limiter

Não há endpoint para ver o estado do rate limiter. Para depurar, adicione temporariamente:

```go
// No worker, antes do Wait:
logger.Debug("rate limiter",
    "canal", string(job.Canal),
    "tokens_disponiveis", limitador.Tokens(),
)
```

---

## Ciclo de Deploy

```
Desenvolvimento local
    │ go run ./cmd/server/
    │ docker-compose up -d
    ▼
Teste e validação
    │ curl endpoints
    │ go test ./...
    ▼
Build da imagem
    │ docker build -t lead-gen-motor:latest .
    │ docker images lead-gen-motor (verificar < 50MB)
    ▼
Deploy na EC2
    │ ./scripts/deploy.sh
    │ ou: scp + docker-compose up -d --build app
    ▼
Verificação pós-deploy
    │ curl http://IP:8080/api/v1/health
    │ curl http://IP:8080/api/v1/status
    │ docker-compose logs -f app
```

---

## Próximas Evoluções Recomendadas (por Prioridade)

### Sprint 1 — Segurança e Confiabilidade

1. **Autenticação JWT** no `cmd/server/main.go` — middleware `auth` antes de todas as rotas
2. **Deduplicação de leads** — `BuscarPorEmail()` antes de `Salvar()` no handler
3. **Consentimento LGPD** — campo `consentimento_lgpd bool + timestamp` na entidade Lead

### Sprint 2 — Observabilidade

4. **Métricas Prometheus** — contador de jobs/hora por canal, taxa de sucesso, latência
5. **Dashboard CloudWatch** — criar via Terraform com métricas do namespace `LeadGenMotor`
6. **Tracing de mensagens** — ID único de correlação propagado por toda a stack

### Sprint 3 — Funcionalidades de Negócio

7. **Importação CSV** — `POST /leads/importar` para carregar listas em bulk
8. **Agendamento de envio** — não enviar fora do horário comercial configurável
9. **Templates avançados** — variáveis dinâmicas adicionais (`{{area}}`, `{{beneficio}}`)

### Sprint 4 — Escala

10. **Lambda de Start/Stop** — função Lambda que inicia/para a EC2 via API HTTP
11. **Múltiplas instâncias** — mover state do Dispatcher para Redis para escalar horizontalmente
12. **ECR + GitHub Actions** — pipeline CI/CD automático com push para ECR

Cada Sprint deve resultar em um PR com documentação atualizada nesta pasta `docs/`.
