-- ============================================================
-- Lead Gen Motor — Schema PostgreSQL
-- Migração 001: Estrutura inicial
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm"; -- busca fuzzy em nomes/emails

-- ============================================================
-- Tipos Enumerados
-- ============================================================

CREATE TYPE canal_tipo AS ENUM (
    'email',
    'whatsapp',
    'linkedin',
    'instagram',
    'facebook',
    'sms'
);

CREATE TYPE status_lead AS ENUM (
    'novo',        -- recém adicionado, não contatado
    'contatado',   -- mensagem enviada, sem resposta
    'respondeu',   -- respondeu alguma mensagem
    'quente',      -- pontuação >= threshold, pronto para closer
    'convertido',  -- fechou negócio
    'descartado'   -- não tem interesse / inválido
);

CREATE TYPE status_campanha AS ENUM (
    'rascunho',
    'ativa',
    'pausada',
    'concluida',
    'arquivada'
);

CREATE TYPE status_job AS ENUM (
    'pendente',
    'processando',
    'enviado',
    'falhou',
    'cancelado'
);

CREATE TYPE status_mensagem AS ENUM (
    'pendente',
    'enviado',
    'entregue',
    'aberto',
    'clicado',
    'respondido',
    'falhou'
);

-- ============================================================
-- Tabela: leads
-- ============================================================

CREATE TABLE leads (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nome             VARCHAR(255) NOT NULL,
    email            VARCHAR(255),
    telefone         VARCHAR(50),
    linkedin_url     VARCHAR(500),
    instagram_handle VARCHAR(100),
    facebook_url     VARCHAR(500),
    empresa          VARCHAR(255),
    cargo            VARCHAR(255),
    website          VARCHAR(500),
    localizacao      VARCHAR(255),

    status           status_lead NOT NULL DEFAULT 'novo',
    pontuacao        INTEGER NOT NULL DEFAULT 0
                     CHECK (pontuacao >= 0 AND pontuacao <= 100),

    canal_preferido  canal_tipo,
    notas            TEXT,
    tags             TEXT[] DEFAULT '{}',

    criado_em        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ultimo_contato_em TIMESTAMPTZ
);

-- ============================================================
-- Tabela: campanhas
-- ============================================================

CREATE TABLE campanhas (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nome             VARCHAR(255) NOT NULL,
    descricao        TEXT,
    status           status_campanha NOT NULL DEFAULT 'rascunho',

    canais           canal_tipo[] NOT NULL DEFAULT '{}',
    template_assunto VARCHAR(500),
    template_corpo   TEXT NOT NULL,

    max_leads        INTEGER DEFAULT 0 CHECK (max_leads >= 0),
    leads_processados INTEGER NOT NULL DEFAULT 0,

    criado_em        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    iniciado_em      TIMESTAMPTZ,
    finalizado_em    TIMESTAMPTZ
);

-- ============================================================
-- Tabela: jobs (fila de trabalho do Dispatcher)
-- ============================================================

CREATE TABLE jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lead_id         UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    campanha_id     UUID NOT NULL REFERENCES campanhas(id) ON DELETE CASCADE,
    canal           canal_tipo NOT NULL,

    prioridade      SMALLINT NOT NULL DEFAULT 5
                    CHECK (prioridade BETWEEN 1 AND 10),
    status          status_job NOT NULL DEFAULT 'pendente',

    tentativas      SMALLINT NOT NULL DEFAULT 0,
    max_tentativas  SMALLINT NOT NULL DEFAULT 3,

    erro            TEXT,
    dados_extras    JSONB DEFAULT '{}',

    criado_em       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processado_em   TIMESTAMPTZ,

    -- Garante que um lead não receba o mesmo job duplicado por campanha/canal
    UNIQUE (lead_id, campanha_id, canal)
);

-- ============================================================
-- Tabela: mensagens (histórico de envios)
-- ============================================================

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

-- ============================================================
-- Tabela: interacoes (rastreamento de engajamento)
-- ============================================================

CREATE TABLE interacoes (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lead_id  UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    tipo     VARCHAR(100) NOT NULL,
    -- tipos: 'abriu_email', 'clicou_link', 'respondeu_email',
    --        'respondeu_whatsapp', 'conectou_linkedin', 'seguiu_instagram'
    canal    canal_tipo,
    dados    JSONB DEFAULT '{}',
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Índices para Performance
-- ============================================================

-- Jobs: Dispatcher busca por status + prioridade + criado_em
CREATE INDEX idx_jobs_despacho ON jobs(status, prioridade DESC, criado_em ASC)
    WHERE status = 'pendente';

-- Leads: filtros comuns
CREATE INDEX idx_leads_status ON leads(status);
CREATE INDEX idx_leads_pontuacao ON leads(pontuacao DESC);
CREATE INDEX idx_leads_email ON leads(email) WHERE email IS NOT NULL;
CREATE INDEX idx_leads_telefone ON leads(telefone) WHERE telefone IS NOT NULL;

-- Mensagens: histórico por lead
CREATE INDEX idx_mensagens_lead ON mensagens(lead_id, criado_em DESC);

-- Interações: lead scoring query
CREATE INDEX idx_interacoes_lead ON interacoes(lead_id, criado_em DESC);

-- Busca fuzzy de leads por nome
CREATE INDEX idx_leads_nome_trgm ON leads USING GIN (nome gin_trgm_ops);

-- ============================================================
-- Trigger: atualiza campo atualizado_em automaticamente
-- ============================================================

CREATE OR REPLACE FUNCTION fn_atualizar_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.atualizado_em = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tg_leads_atualizado
    BEFORE UPDATE ON leads
    FOR EACH ROW EXECUTE FUNCTION fn_atualizar_timestamp();

CREATE TRIGGER tg_campanhas_atualizado
    BEFORE UPDATE ON campanhas
    FOR EACH ROW EXECUTE FUNCTION fn_atualizar_timestamp();

CREATE TRIGGER tg_jobs_atualizado
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION fn_atualizar_timestamp();

-- ============================================================
-- Dados Iniciais: campanha de exemplo
-- ============================================================

INSERT INTO campanhas (nome, descricao, canais, template_assunto, template_corpo)
VALUES (
    'Prospecção Inicial B2B',
    'Campanha de aquecimento para novos leads via email + LinkedIn',
    ARRAY['email', 'linkedin']::canal_tipo[],
    'Oportunidade de parceria — {{empresa}}',
    'Olá {{nome}},

Encontrei o perfil da {{empresa}} e acredito que temos uma sinergia interessante na área de tecnologia e consultoria.

Trabalho com soluções que ajudaram empresas do segmento a reduzir custos operacionais e escalar com mais eficiência.

Seria possível conversar 15 minutos esta semana para eu mostrar como isso funcionaria para a {{empresa}}?

Att,
Demarchi MEI — demarchivagas@gmail.com'
);
