// Package config carrega e valida a configuração da aplicação.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config é a configuração completa da aplicação.
type Config struct {
	Servidor  ServidorConfig
	Banco     BancoConfig
	AWS       AWSConfig
	WhatsApp  WhatsAppConfig
	Chromedp  ChromedpConfig
	Limites   LimitesConfig
	Workers   WorkersConfig
	Scoring   ScoringConfig
	Social    SocialConfig
	Notif     NotifConfig
}

type ServidorConfig struct {
	Porta           int
	Ambiente        string
	TimeoutLeitura  time.Duration
	TimeoutEscrita  time.Duration
	TimeoutIdle     time.Duration
}

type BancoConfig struct {
	Host     string
	Porta    int
	Nome     string
	Usuario  string
	Senha    string
	SSLMode  string
	MaxConns int
	MaxIdle  int
}

type AWSConfig struct {
	Regiao         string
	SESRemetente   string
	SESNomeDisplay string
}

type WhatsAppConfig struct {
	APIURL      string
	Token       string
	PhoneID     string
}

type ChromedpConfig struct {
	ChromeURL   string
	DelayMinMS  int
	DelayMaxMS  int
	LinkedInUser string
	LinkedInPass string
	InstaUser   string
	InstaPass   string
	FBUser      string
	FBPass      string
}

type NotifConfig struct {
	SNSNumeroCloser string
	SNSNomeDisplay  string
}

type SocialConfig struct {
	LinkedIn ChromedpConfig
	Instagram ChromedpConfig
	Facebook  ChromedpConfig
}

// LimitesConfig controla o rate limiting por canal — carregado do limits.yaml.
type LimitesConfig struct {
	Email     LimiteCanal `yaml:"email"`
	WhatsApp  LimiteCanal `yaml:"whatsapp"`
	LinkedIn  LimiteCanal `yaml:"linkedin"`
	Instagram LimiteCanal `yaml:"instagram"`
	Facebook  LimiteCanal `yaml:"facebook"`
	SMS       LimiteCanal `yaml:"sms"`
}

type LimiteCanal struct {
	PorHora              int `yaml:"por_hora"`
	PorDia               int `yaml:"por_dia"`
	BurstMax             int `yaml:"burst_max"`
	IntervaloMinSegundos int `yaml:"intervalo_min_segundos"`
}

type WorkersConfig struct {
	Quantidade          int           `yaml:"quantidade"`
	TamanhoFila         int           `yaml:"tamanho_fila"`
	IntervaloDespacho   time.Duration `yaml:"intervalo_despacho"`
	MaxJobsPorDespacho  int           `yaml:"max_jobs_por_despacho"`
}

type ScoringConfig struct {
	ThresholdLeadQuente int `yaml:"threshold_lead_quente"`
	PontosResposta      int `yaml:"pontos_resposta"`
	PontosClique        int `yaml:"pontos_clique"`
	PontosInteracao     int `yaml:"pontos_interacao_adicional"`
	PontosCampo         int `yaml:"pontos_campo_preenchido"`
}

// arquivoLimites é a estrutura mapeada do limits.yaml.
type arquivoLimites struct {
	Limites LimitesConfig `yaml:"limites"`
	Workers struct {
		Quantidade              int `yaml:"quantidade"`
		TamanhoFila             int `yaml:"tamanho_fila"`
		IntervaloDespachoSegundos int `yaml:"intervalo_despacho_segundos"`
		MaxJobsPorDespacho      int `yaml:"max_jobs_por_despacho"`
	} `yaml:"workers"`
	Pontuacao ScoringConfig `yaml:"pontuacao"`
}

// Carregar lê variáveis de ambiente e o arquivo limits.yaml.
func Carregar(caminhoLimites string) (*Config, error) {
	cfg := &Config{}

	// --- Servidor ---
	cfg.Servidor.Porta = envInt("APP_PORTA", 8080)
	cfg.Servidor.Ambiente = envStr("APP_AMBIENTE", "prod")
	cfg.Servidor.TimeoutLeitura = 10 * time.Second
	cfg.Servidor.TimeoutEscrita = 30 * time.Second
	cfg.Servidor.TimeoutIdle = 60 * time.Second

	// --- Banco ---
	cfg.Banco.Host    = envStr("DB_HOST", "localhost")
	cfg.Banco.Porta   = envInt("DB_PORTA", 5432)
	cfg.Banco.Nome    = envStr("DB_NOME", "leadgen")
	cfg.Banco.Usuario = envStr("DB_USUARIO", "leadgen")
	cfg.Banco.Senha   = envStr("DB_SENHA", "")
	cfg.Banco.SSLMode = envStr("DB_SSLMODE", "disable")
	cfg.Banco.MaxConns = envInt("DB_MAX_CONNS", 10)
	cfg.Banco.MaxIdle  = envInt("DB_MAX_IDLE", 5)

	// --- AWS ---
	cfg.AWS.Regiao        = envStr("AWS_REGION", "us-east-1")
	cfg.AWS.SESRemetente  = envStr("SES_REMETENTE_EMAIL", "")
	cfg.AWS.SESNomeDisplay = envStr("SES_REMETENTE_NOME", "Lead Gen Motor")

	// --- WhatsApp ---
	cfg.WhatsApp.APIURL  = envStr("WHATSAPP_API_URL", "https://graph.facebook.com/v18.0")
	cfg.WhatsApp.Token   = envStr("WHATSAPP_TOKEN", "")
	cfg.WhatsApp.PhoneID = envStr("WHATSAPP_PHONE_ID", "")

	// --- Chromedp ---
	cfg.Chromedp.ChromeURL    = envStr("CHROME_URL", "ws://chromedp:9222")
	cfg.Chromedp.DelayMinMS   = envInt("CHROME_DELAY_MIN_MS", 500)
	cfg.Chromedp.DelayMaxMS   = envInt("CHROME_DELAY_MAX_MS", 2000)
	cfg.Chromedp.LinkedInUser = envStr("LINKEDIN_USER", "")
	cfg.Chromedp.LinkedInPass = envStr("LINKEDIN_PASS", "")
	cfg.Chromedp.InstaUser    = envStr("INSTAGRAM_USER", "")
	cfg.Chromedp.InstaPass    = envStr("INSTAGRAM_PASS", "")
	cfg.Chromedp.FBUser       = envStr("FACEBOOK_USER", "")
	cfg.Chromedp.FBPass       = envStr("FACEBOOK_PASS", "")

	// --- Notificação ---
	cfg.Notif.SNSNumeroCloser = envStr("SNS_NUMERO_CLOSER", "")
	cfg.Notif.SNSNomeDisplay  = envStr("SNS_NOME_DISPLAY", "LeadGen")

	// --- Limites e Workers (do YAML) ---
	if caminhoLimites != "" {
		if err := carregarLimites(cfg, caminhoLimites); err != nil {
			return nil, err
		}
	} else {
		cfg.aplicarDefaults()
	}

	// --- Validação ---
	if cfg.Banco.Senha == "" {
		return nil, fmt.Errorf("config: DB_SENHA é obrigatória")
	}

	return cfg, nil
}

func carregarLimites(cfg *Config, caminho string) error {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return fmt.Errorf("config: falha ao ler %s: %w", caminho, err)
	}

	var arquivo arquivoLimites
	if err := yaml.Unmarshal(dados, &arquivo); err != nil {
		return fmt.Errorf("config: YAML inválido em %s: %w", caminho, err)
	}

	cfg.Limites = arquivo.Limites
	cfg.Workers.Quantidade = arquivo.Workers.Quantidade
	cfg.Workers.TamanhoFila = arquivo.Workers.TamanhoFila
	cfg.Workers.MaxJobsPorDespacho = arquivo.Workers.MaxJobsPorDespacho
	cfg.Workers.IntervaloDespacho = time.Duration(arquivo.Workers.IntervaloDespachoSegundos) * time.Second
	cfg.Scoring = arquivo.Pontuacao

	if cfg.Workers.Quantidade <= 0 {
		cfg.Workers.Quantidade = 5
	}
	if cfg.Workers.TamanhoFila <= 0 {
		cfg.Workers.TamanhoFila = 100
	}
	if cfg.Workers.IntervaloDespacho <= 0 {
		cfg.Workers.IntervaloDespacho = 30 * time.Second
	}

	return nil
}

func (c *Config) aplicarDefaults() {
	c.Limites = LimitesConfig{
		Email:     LimiteCanal{PorHora: 50,  PorDia: 200, BurstMax: 5},
		WhatsApp:  LimiteCanal{PorHora: 10,  PorDia: 50,  BurstMax: 2},
		LinkedIn:  LimiteCanal{PorHora: 5,   PorDia: 20,  BurstMax: 1},
		Instagram: LimiteCanal{PorHora: 5,   PorDia: 20,  BurstMax: 1},
		Facebook:  LimiteCanal{PorHora: 5,   PorDia: 20,  BurstMax: 1},
		SMS:       LimiteCanal{PorHora: 20,  PorDia: 100, BurstMax: 3},
	}
	c.Workers = WorkersConfig{
		Quantidade:         5,
		TamanhoFila:        100,
		IntervaloDespacho:  30 * time.Second,
		MaxJobsPorDespacho: 50,
	}
	c.Scoring = ScoringConfig{
		ThresholdLeadQuente: 60,
		PontosResposta:      30,
		PontosClique:        20,
		PontosInteracao:     10,
		PontosCampo:         5,
	}
}

func envStr(chave, padrao string) string {
	if v := os.Getenv(chave); v != "" {
		return v
	}
	return padrao
}

func envInt(chave string, padrao int) int {
	if v := os.Getenv(chave); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return padrao
}
