# Banco de Dados — Lead Gen Motor

## Tecnologia

**PostgreSQL 16** rodando em container Alpine (`postgres:16-alpine`).

Escolhas de design:
- **UUIDs como PK** (`gen_random_uuid()`): evita conflito em merges de dados, não expõe sequência
- **TIMESTAMPTZ**: todos os timestamps em UTC com timezone
- **Tipos ENUM**: integridade de dados sem precisar de tabela de lookup
- **JSONB**: dados extras flexíveis sem schema rígido (interações, metadados)

---

## Schema Completo

### Tipos Enumerados

```sql
-- Canal de comunicação com o lead
canal_tipo: email | whatsapp | linkedin | instagram | facebook | sms

-- Estado do lead no funil
status_lead: novo | contatado | respondeu | quente | convertido | descartado

-- Estado da campanha
status_campanha: rascunho | ativa | pausada | concluida | arquivada

-- Estado do job na fila
status_job: pendente | processando | enviado | falhou | cancelado

-- Estado de uma mensagem individual
status_mensagem: pendente | enviado | entregue | aberto | clicado | respondido | falhou
```

### Tabela `leads`

```sql
CREATE TABLE leads (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nome             VARCHAR(255) NOT NULL,
    email            VARCHAR(255),           -- único quando não nulo
    telefone         VARCHAR(50),
    linkedin_url     VARCHAR(500),
    instagram_handle VARCHAR(100),
    facebook_url     VARCHAR(500),
    empresa          VARCHAR(255),
    cargo            VARCHAR(255),
    website          VARCHAR(500),
    localizacao      VARCHAR(255),

    status           status_lead NOT NULL DEFAULT 'novo',
    pontuacao        INTEGER NOT NULL DEFAULT 0 CHECK (pontuacao BETWEEN 0 AND 100),

    canal_preferido  canal_tipo,
    notas            TEXT,
    tags             TEXT[] DEFAULT '{}',

    criado_em        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ultimo_contato_em TIMESTAMPTZ
);
```

**Índices:**
```sql
idx_leads_status          -- filtros por status (listagem, dispatcher)
idx_leads_pontuacao       -- ordenação por score
idx_leads_email           -- deduplicação (WHERE email IS NOT NULL)
idx_leads_telefone        -- busca por telefone
idx_leads_nome_trgm       -- busca fuzzy por nome (pg_trgm)
```

### Tabela `campanhas`

```sql
CREATE TABLE campanhas (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nome             VARCHAR(255) NOT NULL,
    descricao        TEXT,
    status           status_campanha NOT NULL DEFAULT 'rascunho',

    canais           canal_tipo[] NOT NULL DEFAULT '{}',  -- array de canais habilitados
    template_assunto VARCHAR(500),
    template_corpo   TEXT NOT NULL,

    max_leads        INTEGER DEFAULT 0 CHECK (max_leads >= 0),  -- 0 = sem limite
    leads_processados INTEGER NOT NULL DEFAULT 0,

    criado_em        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    iniciado_em      TIMESTAMPTZ,
    finalizado_em    TIMESTAMPTZ
);
```

### Tabela `jobs`

```sql
CREATE TABLE jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lead_id         UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    campanha_id     UUID NOT NULL REFERENCES campanhas(id) ON DELETE CASCADE,
    canal           canal_tipo NOT NULL,

    prioridade      SMALLINT NOT NULL DEFAULT 5 CHECK (prioridade BETWEEN 1 AND 10),
    status          status_job NOT NULL DEFAULT 'pendente',

    tentativas      SMALLINT NOT NULL DEFAULT 0,
    max_tentativas  SMALLINT NOT NULL DEFAULT 3,

    erro            TEXT,
    dados_extras    JSONB DEFAULT '{}',

    criado_em       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processado_em   TIMESTAMPTZ,

    UNIQUE (lead_id, campanha_id, canal)  -- sem duplicatas por (lead, campanha, canal)
);
```

**Índice crítico para o Dispatcher:**
```sql
CREATE INDEX idx_jobs_despacho ON jobs(status, prioridade DESC, criado_em ASC)
    WHERE status = 'pendente';
-- Índice parcial — só indexa jobs pendentes, é pequeno e rápido
```

### Tabela `mensagens`

Histórico imutável de todos os envios. Nunca atualizado — apenas inserido.

```sql
CREATE TABLE mensagens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id      UUID NOT NULL REFERENCES jobs(id),
    lead_id     UUID NOT NULL REFERENCES leads(id),
    campanha_id UUID NOT NULL REFERENCES campanhas(id),
    canal       canal_tipo NOT NULL,

    assunto     VARCHAR(500),
    corpo       TEXT NOT NULL,
    status      status_mensagem NOT NULL DEFAULT 'pendente',

    criado_em   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    enviado_em  TIMESTAMPTZ
);
```

### Tabela `interacoes`

Rastreamento de engajamento — base do lead scoring.

```sql
CREATE TABLE interacoes (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lead_id   UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    tipo      VARCHAR(100) NOT NULL,
    -- abriu_email | clicou_link | respondeu_email | respondeu_whatsapp
    -- respondeu_linkedin | conectou_linkedin | seguiu_instagram | visitou_perfil
    canal     canal_tipo,
    dados     JSONB DEFAULT '{}',  -- metadados livres (URL clicada, assunto, etc.)
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## Diagrama de Relacionamentos (ER)

```
leads (1) ──────────────────────────── (N) jobs
  │                                          │
  │                                          │
  └─── (N) interacoes              campanhas (1) ─── (N) jobs
  │
  └─── (N) mensagens ─── jobs (1)
                    └─── campanhas (1)
```

---

## Triggers

### `fn_atualizar_timestamp` + triggers

Atualiza automaticamente `atualizado_em` em qualquer UPDATE:

```sql
CREATE OR REPLACE FUNCTION fn_atualizar_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.atualizado_em = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Aplicado em: leads, campanhas, jobs
CREATE TRIGGER tg_leads_atualizado
    BEFORE UPDATE ON leads
    FOR EACH ROW EXECUTE FUNCTION fn_atualizar_timestamp();
```

---

## Queries Importantes

### Dispatcher: busca jobs com locking

```sql
-- Seleciona jobs pendentes de campanhas ativas, com lock para evitar duplicatas
SELECT j.id, j.lead_id, j.campanha_id, j.canal, j.prioridade,
       j.tentativas, j.max_tentativas,
       l.id, l.nome, l.email, l.telefone, l.linkedin_url,
       l.instagram_handle, l.facebook_url, l.empresa, l.status, l.pontuacao,
       c.id, c.nome, c.template_assunto, c.template_corpo
FROM jobs j
JOIN leads l ON l.id = j.lead_id
JOIN campanhas c ON c.id = j.campanha_id
WHERE j.status = 'pendente'
  AND c.status = 'ativa'
ORDER BY j.prioridade DESC, j.criado_em ASC
LIMIT 50
FOR UPDATE OF j SKIP LOCKED;
```

### Lead Scoring: contar interações

```sql
SELECT COUNT(*) FROM interacoes WHERE lead_id = $1;
```

### Dashboard: leads por status

```sql
SELECT status, COUNT(*), AVG(pontuacao)::int as score_medio
FROM leads
GROUP BY status
ORDER BY
  CASE status
    WHEN 'quente' THEN 1
    WHEN 'respondeu' THEN 2
    WHEN 'contatado' THEN 3
    WHEN 'novo' THEN 4
    WHEN 'convertido' THEN 5
    WHEN 'descartado' THEN 6
  END;
```

### Jobs por canal hoje

```sql
SELECT canal, status, COUNT(*)
FROM jobs
WHERE criado_em >= CURRENT_DATE
GROUP BY canal, status
ORDER BY canal, status;
```

---

## Política de Migrations

### Naming

```
migrations/
├── 001_schema_inicial.sql        ← schema completo inicial
├── 002_adicionar_telegram.sql    ← adiciona canal telegram
├── 003_campo_consentimento.sql   ← LGPD: campo consentimento
└── 004_indices_performance.sql   ← índices extras
```

### Regras

1. **Nunca edite** uma migration já executada em produção — crie uma nova
2. **Sempre idempotente**: use `IF NOT EXISTS`, `CREATE TYPE IF NOT EXISTS`
3. **Sem rollback automático**: se precisar reverter, crie uma migration de reversão
4. **PostgreSQL executa** arquivos de `docker-entrypoint-initdb.d/` **apenas uma vez** (quando o volume está vazio)

### Para aplicar em produção (após o banco estar rodando)

```bash
docker-compose exec -T postgres \
  psql -U leadgen leadgen < migrations/002_nova_feature.sql
```

---

## Backup e Recuperação

### Backup Completo

```bash
# Backup comprimido
docker-compose exec -T postgres \
  pg_dump -U leadgen leadgen | gzip > backup_$(date +%Y%m%d_%H%M%S).sql.gz

# Backup apenas dos leads (sem schema)
docker-compose exec -T postgres \
  pg_dump -U leadgen leadgen --data-only --table=leads | gzip > leads_backup.sql.gz
```

### Restauração

```bash
# Restauração completa (banco deve existir e estar vazio)
gunzip -c backup_YYYYMMDD_HHMMSS.sql.gz | \
  docker-compose exec -T postgres psql -U leadgen leadgen

# Restauração seletiva
gunzip -c leads_backup.sql.gz | \
  docker-compose exec -T postgres psql -U leadgen leadgen
```

### Limpeza de Dados Antigos

```sql
-- Remove jobs cancelados com mais de 30 dias
DELETE FROM jobs
WHERE status = 'cancelado'
  AND atualizado_em < NOW() - INTERVAL '30 days';

-- Remove interações com mais de 90 dias (preserva scoring atual)
DELETE FROM interacoes
WHERE criado_em < NOW() - INTERVAL '90 days';
```
