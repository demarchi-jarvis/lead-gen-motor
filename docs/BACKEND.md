# Backend Go — Lead Gen Motor

## Stack e Dependências

```
Go 1.22
├── stdlib (net/http, log/slog, database/sql, context, sync)
├── golang.org/x/time/rate     — token bucket rate limiting
├── github.com/aws/aws-sdk-go-v2  — SES + SNS
├── github.com/chromedp/chromedp  — automação headless browser
├── github.com/lib/pq             — driver PostgreSQL
└── gopkg.in/yaml.v3              — parse do limits.yaml
```

**Por que tão poucas dependências?**  
Cada dependência aumenta o binário e a superfície de ataque. Go tem uma stdlib excelente — usamos ao máximo.

---

## Entidades do Domínio (`pkg/domain/`)

### Lead

A entidade central do sistema. Representa um potencial cliente.

```go
type Lead struct {
    ID              string          // UUID gerado pelo PostgreSQL
    Nome            string          // obrigatório
    Email           string          // opcional, mas necessário para CanalEmail
    Telefone        string          // opcional, necessário para WhatsApp/SMS
    LinkedInURL     string          // opcional, necessário para LinkedIn
    InstagramHandle string          // opcional, ex: "@fulano"
    FacebookURL     string          // opcional
    Empresa         string          // para personalização de templates
    Cargo           string          // para personalização de templates
    Website         string
    Localizacao     string

    Status          StatusLead      // ciclo de vida do lead
    Pontuacao       int             // 0-100, threshold quente = 60
    CanalPreferido  Canal           // canal com mais engajamento
    Notas           string          // observações internas
    Tags            []string        // categorização livre

    CriadoEm        time.Time
    AtualizadoEm    time.Time
    UltimoContatoEm *time.Time
}
```

**Ciclo de vida do Lead:**
```
novo → contatado → respondeu → quente → convertido
                ↘             ↗
               descartado
```

**Métodos de negócio importantes:**
- `EhValido()` — tem pelo menos um canal de contato
- `CanaisDisponiveis()` — retorna quais canais têm dados suficientes
- `AdicionarPontos(n)` — incrementa score, eleva status se >= 60, retorna `true` se ficou quente
- `RegistrarContato()` — atualiza `UltimoContatoEm` e status para `contatado`
- `RegistrarResposta()` — status → `respondeu` + +30 pontos
- `CamposPreenchidos()` — conta campos não-vazios para cálculo de score

### Campanha

Define uma campanha de prospecção com template e canais.

```go
type Campanha struct {
    ID               string
    Nome             string
    Status           StatusCampanha   // rascunho → ativa → pausada → concluida
    Canais           []Canal          // ex: [email, linkedin]
    TemplateAssunto  string           // "Oportunidade — {{empresa}}"
    TemplateCorpo    string           // corpo com variáveis {{nome}}, {{cargo}}, etc.
    MaxLeads         int              // 0 = sem limite
    LeadsProcessados int
}
```

**Variáveis de template disponíveis:**
| Variável | Fonte | Exemplo |
|---|---|---|
| `{{nome}}` | `lead.Nome` | "João Silva" |
| `{{empresa}}` | `lead.Empresa` | "Acme Corp" |
| `{{cargo}}` | `lead.Cargo` | "CTO" |
| `{{email}}` | `lead.Email` | "joao@acme.com" |
| `{{telefone}}` | `lead.Telefone` | "+5511..." |

> **Para adicionar novas variáveis**: edite `RenderizarMensagem()` em `campaign.go`

**Transições de status permitidas:**
- `rascunho → ativa` (via `Ativar()`)
- `pausada → ativa` (via `Ativar()`)
- `ativa → pausada` (via `Pausar()`)
- Qualquer → `concluida` (via `Concluir()`)

### Job

Unidade de trabalho na fila do Dispatcher. Criado quando uma campanha é despachada para leads.

```go
type Job struct {
    Lead          *Lead       // dados completos do lead
    Campanha      *Campanha   // dados completos da campanha
    Canal         Canal       // canal específico deste job
    Prioridade    int         // 1-10 (10 = mais urgente)
    Tentativas    int
    MaxTentativas int         // padrão: 3
}
```

**Estados do job no banco:**
```
pendente → processando → enviado
         ↘             ↗
          cancelado (falha permanente)
         ↗
pendente (falha transiente, max tentativas não atingido)
```

---

## Motor de Envio (`pkg/core/`)

### Dispatcher

```go
type Dispatcher struct {
    filaJobs    chan *domain.Job     // channel buffered (capacidade configurável)
    jobRepo     ports.JobRepositoryPort
    leadRepo    ports.LeadRepositoryPort
    mensageiros map[domain.Canal]ports.MessengerPort
    notificador ports.NotificacaoPort
    scoring     *ServicoScoring
    cfg         ConfigDispatcher
}
```

**Loop principal:**
```go
ticker := time.NewTicker(cfg.IntervaloDespacho)  // padrão: 30s

for {
    select {
    case <-ctx.Done():
        close(filaJobs)  // sinaliza workers para parar
        wg.Wait()        // aguarda todos os workers terminarem
        return nil

    case <-ticker.C:
        despacharJobsPendentes(ctx)
    }
}
```

**Anti-duplicate com `SELECT FOR UPDATE SKIP LOCKED`:**
```sql
SELECT * FROM jobs
WHERE status = 'pendente'
AND (SELECT status FROM campanhas WHERE id = jobs.campanha_id) = 'ativa'
ORDER BY prioridade DESC, criado_em ASC
LIMIT $1
FOR UPDATE OF jobs SKIP LOCKED
```
Isso garante que mesmo com múltiplos workers consultando o banco, cada job é processado exatamente uma vez.

### Worker

Cada Worker tem seu próprio mapa de rate limiters, um por canal:

```go
limitadores := map[domain.Canal]*rate.Limiter{
    CanalEmail:     rate.NewLimiter(50.0/3600, 5),    // 50/h, burst 5
    CanalWhatsApp:  rate.NewLimiter(10.0/3600, 2),    // 10/h, burst 2
    CanalLinkedIn:  rate.NewLimiter(5.0/3600,  1),    // 5/h,  burst 1
    CanalInstagram: rate.NewLimiter(5.0/3600,  1),
    CanalFacebook:  rate.NewLimiter(5.0/3600,  1),
    CanalSMS:       rate.NewLimiter(20.0/3600, 3),
}
```

**O `rate.Limiter.Wait(ctx)` é bloqueante** — o Worker fica na fila do limiter até ter permissão. Isso nunca bloqueia outros Workers processando outros canais.

### ServicoScoring

O algoritmo de scoring é **determinístico e auditável**:

```
score = 0
+ (campos_preenchidos × 5)    → máximo 30 pontos (6 campos × 5)
+ (30 se status == respondeu)
+ (5 se status == contatado)
+ ((interacoes - 1) × 10)     → máximo 20 pontos extras
= máximo 80 pontos via engajamento

threshold_quente = 60
```

**Exemplo prático:**
- Lead tem email + telefone + linkedin + empresa (4 campos) → 4×5 = 20 pontos
- Lead respondeu um email → +30 pontos
- Total: 50 pontos → ainda não é quente
- Lead interagiu 2x mais → +(2×10) = +20 pontos
- Total: 70 pontos → **QUENTE** → closer recebe SMS

---

## Adapters de Mensagem (`pkg/adapters/`)

### Adapter SES (`email/ses.go`)

**Fluxo de retry:**
```
SendEmail() falhou?
├── MessageRejected → DeveRetentar=false (email permanentemente inválido)
├── InvalidParameterValue → DeveRetentar=false
└── Qualquer outro → DeveRetentar=true (throttling, timeout, etc.)
```

**Formato de envio:**
- Texto plano + HTML gerado automaticamente do texto
- Remetente: `"Nome <email@dominio.com>"` ou apenas `"email@dominio.com"`

### Adapter WhatsApp (`whatsapp/webhook.go`)

Usa a **Meta Business Cloud API** (Graph API v18.0+).

**Requisitos:**
- Conta WhatsApp Business verificada
- Token de acesso permanente (não temporário)
- O destinatário deve ter enviado mensagem para o número nas últimas 24h OU usar template aprovado

**Normalização de telefone:**
```
"(11) 99999-9999" → "5511999999999"
"+55 11 99999-9999" → "5511999999999"
"11999999999" → "5511999999999" (adiciona DDI Brasil)
```

**Códigos de erro Meta não-retrytable:**
- `131030` — número não existe no WhatsApp
- `131031` — número bloqueou a empresa

### Adapter Social (`social/chromedp.go`)

Conecta ao **Chrome headless remoto** (container Docker separado) via CDP (Chrome DevTools Protocol).

**Configuração do Chrome remoto:**
```
CHROME_URL=ws://chromedp:9222
```

**Anti-detecção:**
- User Agent: Chrome real (não "HeadlessChrome")
- Delays aleatórios entre ações: 500-2000ms (configurável)
- Uma sessão por job (não reutiliza browser state entre leads)

**Importante sobre LinkedIn:**
- Limite seguro: 5 conexões/hora, 20/dia
- Primeiro tenta botão "Connect" (novo lead) → com nota personalizada
- Se já conectado: tenta "Message" (mensagem direta)
- Nota de conexão: máximo 300 caracteres (limite da plataforma)

### Adapter SNS (`sms/sns.go`)

Dupla função:
1. **`MessengerPort`**: envia SMS diretamente para o lead (canal SMS)
2. **`NotificacaoPort`**: envia SMS para o *closer* quando um lead fica quente

**Tipo de SMS**: `Transactional` (entrega garantida, mais caro que Promotional).

---

## Repositório PostgreSQL (`pkg/repository/postgres.go`)

### Conexão Pool

```go
conn.SetMaxOpenConns(10)        // máximo de conexões abertas
conn.SetMaxIdleConns(5)         // conexões idle mantidas abertas
conn.SetConnMaxLifetime(5min)   // recria conexão a cada 5 min (evita timeouts)
conn.SetConnMaxIdleTime(2min)   // fecha idle após 2 min
```

### Padrão de Queries

Todas as queries usam `context.Context` propagado da requisição HTTP:
```go
r.db.conn.QueryRowContext(ctx, query, args...)
r.db.conn.QueryContext(ctx, query, args...)
r.db.conn.ExecContext(ctx, query, args...)
```

### Compile-time Interface Check

```go
// Garante que *LeadRepository implementa LeadRepositoryPort
// Falha em tempo de compilação se a interface não estiver completa
var _ ports.LeadRepositoryPort = (*LeadRepository)(nil)
```

---

## API HTTP

### Servidor

```go
servidor := &http.Server{
    Addr:         ":8080",
    Handler:      mux,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 30 * time.Second,
    IdleTimeout:  60 * time.Second,
}
```

**Roteamento:** usa `http.ServeMux` com pattern matching do Go 1.22 (suporte a `{id}` e métodos HTTP).

### Graceful Shutdown

```go
// Captura SIGTERM (Kubernetes/ECS) e SIGINT (Ctrl+C)
signal.Notify(sinais, syscall.SIGTERM, syscall.SIGINT)

// Ao receber sinal:
cancelCtx()                                          // para Dispatcher e Workers
servidor.Shutdown(ctx com timeout de 30s)            // termina conexões HTTP abertas
```

---

## Configuração (`config/`)

### Hierarquia de Configuração

1. **`config/limits.yaml`** — rate limits, workers, scoring (versionado no Git)
2. **Variáveis de ambiente** — credenciais, hosts, senhas (NÃO versionado)
3. **Valores padrão** no código (fallback seguro)

### Arquivo `limits.yaml` — Tuning

Editar este arquivo e reiniciar a aplicação é suficiente para ajustar:
- Rate limits por canal (sem recompilar)
- Número de workers
- Intervalo de despacho
- Thresholds de pontuação

---

## Logging

Todos os logs usam `log/slog` com campos estruturados:

```go
logger.Info("worker: mensagem enviada com sucesso",
    "lead_id", job.Lead.ID,
    "canal",   string(job.Canal),
    "id_externo", resultado.IDExterno,
)
```

**Em produção** (`APP_AMBIENTE=prod`): JSON para ingestão no CloudWatch.  
**Em desenvolvimento** (`APP_AMBIENTE=dev`): texto legível + nível DEBUG.

---

## Adicionando um Novo Canal — Passo a Passo

Exemplo: adicionar Telegram.

**1.** Adicione a constante em `pkg/domain/lead.go`:
```go
const CanalTelegram Canal = "telegram"
```

**2.** Adicione campo no Lead:
```go
type Lead struct {
    // ...
    TelegramUsername string
}
```

**3.** Crie o adapter `pkg/adapters/telegram/adapter.go`:
```go
package telegram

type AdaptadorTelegram struct { /* ... */ }

func (a *AdaptadorTelegram) Canal() domain.Canal { return domain.CanalTelegram }
func (a *AdaptadorTelegram) Disponivel() bool    { return a.cfg.Token != "" }
func (a *AdaptadorTelegram) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
    // implementação
}
```

**4.** Registre no `cmd/server/main.go`:
```go
if cfg.Telegram.Token != "" {
    tgAdapter, _ := telegram.Novo(...)
    mensageiros[domain.CanalTelegram] = tgAdapter
}
```

**5.** Adicione limites em `config/limits.yaml`:
```yaml
limites:
  telegram:
    por_hora: 30
    por_dia: 200
    burst_max: 5
```

**6.** Adicione ao enum SQL e à migration:
```sql
ALTER TYPE canal_tipo ADD VALUE 'telegram';
```

**7.** Atualize `LimitesDefault` em `pkg/core/worker.go`.

**Total de arquivos editados: 5** (adapter novo + 4 ajustes). O core, os outros adapters e o banco de dados não mudam.
