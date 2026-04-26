# Arquitetura do Sistema — Lead Gen Motor

## Visão Geral

O Lead Gen Motor usa **Arquitetura Hexagonal** (também chamada de *Ports & Adapters* ou *Clean Architecture*). O princípio fundamental é que o **domínio de negócio não sabe da existência do mundo externo** — banco de dados, APIs, frameworks.

```
┌─────────────────────────────────────────────────────────────────┐
│                        MUNDO EXTERNO                            │
│   SES │ WhatsApp API │ LinkedIn │ Instagram │ Facebook │ SNS    │
│   PostgreSQL │ HTTP Clients │ Chrome/chromedp                   │
└──────────────────────────┬──────────────────────────────────────┘
                           │  implementa interfaces
                           ▼
┌─────────────────────────────────────���───────────────────────────┐
│                    ADAPTERS (pkg/adapters/)                      │
│   email/ses.go │ whatsapp/webhook.go │ social/chromedp.go       │
│   sms/sns.go   │ repository/postgres.go                         │
└──────────────────────────┬──────────────────────────────────────┘
                           │  implementam
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                    PORTS (pkg/ports/)                            │
│   MessengerPort │ NotificacaoPort                               │
│   LeadRepositoryPort │ CampanhaRepositoryPort │ JobRepositoryPort│
└──────────────────────────┬──────────────────────────────────────┘
                           │  usam
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                    CORE / APPLICATION (pkg/core/)                │
│   Dispatcher (produtor) │ Worker (consumidor) │ ServicoScoring  │
└──────────────────────────┬──────────────────────────────────────┘
                           │  opera sobre
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                    DOMAIN (pkg/domain/)                          │
│   Lead │ Campanha │ Job │ Mensagem │ Canal │ Status             │
│   Regras de negócio puras — ZERO dependências externas          │
└─────────────────────────────────────────────────────────────────┘
```

---

## Camadas em Detalhe

### 1. Domain (`pkg/domain/`)

A camada mais interna. Contém as **entidades** e **regras de negócio** que existem independente de tecnologia.

**O que tem:**
- `Lead` — entidade principal com todos os dados do prospect
- `Campanha` — define uma campanha multicanal
- `Job` — unidade de trabalho na fila do dispatcher
- `Canal` — tipo enumerado: email, whatsapp, linkedin, instagram, facebook, sms
- `StatusLead` — ciclo de vida: novo → contatado → respondeu → quente → convertido
- Constantes de pontuação (scoring thresholds)
- Métodos de negócio puros: `AdicionarPontos()`, `RegistrarResposta()`, `CanaisDisponiveis()`

**Regra de ouro:** nenhum arquivo em `pkg/domain/` pode importar nada fora da stdlib Go.

### 2. Ports (`pkg/ports/`)

Interfaces que definem os **contratos** entre o core e o mundo externo. São as "portas" da arquitetura hexagonal.

**`MessengerPort`** — qualquer coisa que possa enviar uma mensagem:
```go
type MessengerPort interface {
    Enviar(ctx context.Context, msg MensagemEnvio) ResultadoEnvio
    Canal() domain.Canal
    Disponivel() bool
}
```

**Repositórios** — qualquer coisa que possa persistir dados:
```go
type LeadRepositoryPort interface {
    Salvar(ctx context.Context, lead *domain.Lead) error
    BuscarPorID(ctx context.Context, id string) (*domain.Lead, error)
    Listar(ctx context.Context, filtro FiltroLead) ([]*domain.Lead, int, error)
    // ... etc
}
```

### 3. Core / Application (`pkg/core/`)

Orquestra o fluxo de negócio. **Depende apenas de interfaces (ports), nunca de adapters concretos.**

**Dispatcher** — o produtor:
- Loop com ticker a cada N segundos
- Busca jobs pendentes no banco
- Envia para o channel (buffer)
- Implementa backpressure: se o channel estiver cheio, ignora o ciclo

**Worker** — o consumidor:
- Múltiplas goroutines lendo do mesmo channel
- Cada goroutine tem um `rate.Limiter` por canal (token bucket algorithm)
- Chama o `MessengerPort` correto para cada job
- Atualiza o banco com sucesso/falha
- Delega scoring ao `ServicoScoring`

**ServicoScoring** — o algoritmo de pontuação:
- Calcula score baseado em campos preenchidos + interações
- Eleva status para `quente` quando score >= threshold
- Dispara notificação via `NotificacaoPort` quando lead fica quente

### 4. Adapters (`pkg/adapters/`)

Implementações concretas das interfaces. Cada adapter é isolado e pode ser trocado sem tocar o resto.

| Adapter | Interface Implementada | Tecnologia |
|---|---|---|
| `email/ses.go` | `MessengerPort` | AWS SDK v2 + SES |
| `whatsapp/webhook.go` | `MessengerPort` | HTTP + Meta API |
| `social/chromedp.go` | `MessengerPort` | chromedp (CDP) |
| `sms/sns.go` | `MessengerPort` + `NotificacaoPort` | AWS SDK v2 + SNS |
| `repository/postgres.go` | Todos os `*RepositoryPort` | `database/sql` + `lib/pq` |

### 5. Entry Point (`cmd/server/main.go`)

A cola de tudo. Responsável por:
1. Carregar configuração (ENV + YAML)
2. Instanciar adapters concretos
3. Injetar dependências (DI manual — sem container)
4. Registrar rotas HTTP
5. Iniciar o Dispatcher em goroutine
6. Escutar SIGTERM/SIGINT para graceful shutdown

---

## Padrão Dispatcher/Worker (Produtor-Consumidor)

```
┌─────────────────────────────────────────────────────┐
│                  PostgreSQL                          │
│  jobs WHERE status='pendente' ORDER BY prioridade   │
└─────────────────────┬───────────────────────────────┘
                      │ BuscarPendentes() a cada 30s
                      ▼
┌──────────────────────────���──────────────────────────┐
│              DISPATCHER (1 goroutine)                │
│  • SELECT FOR UPDATE SKIP LOCKED (anti-duplicate)    │
│  • Backpressure: ignora se channel cheio             │
│  • MarcarProcessando() antes de enviar ao channel    │
└─────────────────────┬───────────────────────────────┘
                      │ channel buffered (cap=100)
                      ▼
┌─────────────────────────────────────────────────────┐
│         WORKER POOL (N goroutines, padrão: 5)        │
│                                                      │
│  Worker-1 ──► rate.Limiter[email]     ──► SES        │
│  Worker-2 ──► rate.Limiter[whatsapp]  ──► Meta API   │
│  Worker-3 ──► rate.Limiter[linkedin]  ──► chromedp   │
│  Worker-4 ──► rate.Limiter[instagram] ──► chromedp   │
│  Worker-5 ──► rate.Limiter[sms]       ──► SNS        │
│                                                      │
│  Cada worker tem seu próprio token bucket por canal  │
└─────────────────────────────────────────────────────┘
```

**Por que `SELECT FOR UPDATE SKIP LOCKED`?**  
Com múltiplos workers lendo do banco, sem esse lock duas goroutines poderiam pegar o mesmo job. `SKIP LOCKED` faz os workers pularem linhas já travadas por outro worker, sem bloquear.

**Por que token bucket e não contador simples?**  
O token bucket (implementado em `golang.org/x/time/rate`) garante que a taxa média seja respeitada mesmo com bursts. Se configurado para 5 mensagens/hora no LinkedIn, o limiter libera 1 token a cada ~720 segundos automaticamente.

---

## Fluxo Completo de uma Mensagem

```
1. API recebe POST /api/v1/campanhas/{id}/despachar
   └─ Cria N jobs na tabela jobs (status='pendente')

2. Dispatcher (tick a cada 30s)
   └─ SELECT * FROM jobs WHERE status='pendente' FOR UPDATE SKIP LOCKED
   └─ Envia jobs ao channel
   └─ UPDATE jobs SET status='processando'

3. Worker pega job do channel
   └─ rate.Limiter.Wait(ctx) — bloqueia se taxa estourou
   └─ campanha.RenderizarMensagem(lead) — substitui variáveis no template
   └─ messenger.Enviar(ctx, msg) — chama o adapter correto

4. Adapter executa envio
   └─ Sucesso: retorna ResultadoEnvio{Sucesso: true, IDExterno: "..."}
   └─ Falha permanente: DeveRetentar=false → job vai para 'cancelado'
   └─ Falha transiente: DeveRetentar=true → job volta para 'pendente'

5. Worker atualiza banco
   └─ MarcarEnviado(jobID, idExterno) OU MarcarFalhado(jobID, erro, podeRetentar)
   └─ Atualiza lead.UltimoContatoEm, lead.Status
   └─ ServicoScoring.RecalcularScore(lead)

6. Se lead ficou quente (score >= 60):
   └─ NotificacaoPort.NotificarLeadQuente(lead, campanha)
   └─ SNS envia SMS para o closer com dados do lead
```

---

## Concorrência e Thread Safety

- **Channel buffered**: comunicação segura entre Dispatcher e Workers
- **`sync/atomic`**: contadores de Workers sem mutex
- **`sync.RWMutex`**: estado do Dispatcher (rodando/parado)
- **`context.Context`**: propagado por toda a stack, cancela ao shutdown
- **`SELECT FOR UPDATE SKIP LOCKED`**: locking no banco, não na aplicação

---

## Decisões de Design Documentadas

| Decisão | Alternativa Descartada | Motivo |
|---|---|---|
| Hexagonal Architecture | MVC tradicional | Facilita troca de providers sem tocar core |
| Channel Go nativo | Kafka, RabbitMQ | Evita custo de infra adicional no Academy |
| `database/sql` + `lib/pq` | GORM, sqlx | Sem magic, sem N+1 silencioso, < 50MB final |
| `log/slog` stdlib | zap, logrus | Zero dependência extra, JSON nativo Go 1.21+ |
| `net/http` stdlib | Gin, Echo, Fiber | Binário menor, sem breaking changes de deps |
| `scratch` no Docker | alpine | 20MB vs 80MB — cabe mais no disco da EC2 |
| Spot Instance + persistent | On-demand | ~70% mais barato, `stop` preserva volume EBS |
