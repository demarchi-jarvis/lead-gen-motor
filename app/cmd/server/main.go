// Lead Gen Motor — Omnichannel Lead Prospecting Engine
// Arquitetura: Hexagonal (Ports & Adapters)
// Autor: Demarchi MEI
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/demarchi/lead-gen-motor/config"
	adaptEmail "github.com/demarchi/lead-gen-motor/pkg/adapters/email"
	adaptSMS "github.com/demarchi/lead-gen-motor/pkg/adapters/sms"
	adaptSocial "github.com/demarchi/lead-gen-motor/pkg/adapters/social"
	adaptWA "github.com/demarchi/lead-gen-motor/pkg/adapters/whatsapp"
	"github.com/demarchi/lead-gen-motor/pkg/core"
	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
	"github.com/demarchi/lead-gen-motor/pkg/repository"
)

func main() {
	// Modo health check: faz GET no /health e sai com 0 (ok) ou 1 (falha).
	// Usado pelo HEALTHCHECK do Docker sem precisar de curl/wget na imagem scratch.
	modoHealthCheck := flag.Bool("healthcheck", false, "executa health check e sai")
	flag.Parse()

	if *modoHealthCheck {
		resp, err := http.Get("http://localhost:8080/api/v1/health")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// ============================================================
	// Logger estruturado (JSON em prod, texto em dev)
	// ============================================================
	logger := configurarLogger()

	logger.Info("lead-gen-motor: iniciando",
		"versao", "1.0.0",
		"pid", os.Getpid(),
	)

	// ============================================================
	// Configuração
	// ============================================================
	caminhoLimites := os.Getenv("LIMITS_FILE")
	if caminhoLimites == "" {
		caminhoLimites = "/app/config/limits.yaml"
	}

	cfg, err := config.Carregar(caminhoLimites)
	if err != nil {
		logger.Error("lead-gen-motor: falha ao carregar configuração", "erro", err.Error())
		os.Exit(1)
	}

	logger.Info("configuração carregada",
		"ambiente", cfg.Servidor.Ambiente,
		"porta", cfg.Servidor.Porta,
		"workers", cfg.Workers.Quantidade,
	)

	// ============================================================
	// Banco de Dados
	// ============================================================
	db, err := repository.Novo(repository.ConfigDB{
		Host:     cfg.Banco.Host,
		Porta:    cfg.Banco.Porta,
		Nome:     cfg.Banco.Nome,
		Usuario:  cfg.Banco.Usuario,
		Senha:    cfg.Banco.Senha,
		SSLMode:  cfg.Banco.SSLMode,
		MaxConns: cfg.Banco.MaxConns,
		MaxIdle:  cfg.Banco.MaxIdle,
	})
	if err != nil {
		logger.Error("lead-gen-motor: falha ao conectar ao banco", "erro", err.Error())
		os.Exit(1)
	}
	defer db.Fechar()
	logger.Info("banco de dados conectado", "host", cfg.Banco.Host, "banco", cfg.Banco.Nome)

	// ============================================================
	// Repositórios (Adapters de Persistência)
	// ============================================================
	leadRepo := repository.NovoLeadRepository(db)
	campanhaRepo := repository.NovoCampanhaRepository(db)
	jobRepo := repository.NovoJobRepository(db)

	// ============================================================
	// Messengers (Adapters de Envio)
	// ============================================================
	mensageiros := make(map[domain.Canal]ports.MessengerPort)

	// Email — Amazon SES
	if cfg.AWS.SESRemetente != "" {
		sesAdapter, err := adaptEmail.Novo(adaptEmail.ConfigSES{
			EmailRemetente: cfg.AWS.SESRemetente,
			NomeRemetente:  cfg.AWS.SESNomeDisplay,
			AWSRegiao:      cfg.AWS.Regiao,
		}, logger)
		if err != nil {
			logger.Warn("email SES indisponível", "erro", err.Error())
		} else {
			mensageiros[domain.CanalEmail] = sesAdapter
			logger.Info("adapter email SES: ativo")
		}
	}

	// WhatsApp — Meta Business API
	if cfg.WhatsApp.Token != "" {
		waAdapter, err := adaptWA.Novo(adaptWA.ConfigWhatsApp{
			APIURL:        cfg.WhatsApp.APIURL,
			Token:         cfg.WhatsApp.Token,
			PhoneNumberID: cfg.WhatsApp.PhoneID,
		}, logger)
		if err != nil {
			logger.Warn("whatsapp indisponível", "erro", err.Error())
		} else {
			mensageiros[domain.CanalWhatsApp] = waAdapter
			logger.Info("adapter whatsapp: ativo")
		}
	}

	// LinkedIn — chromedp
	if cfg.Chromedp.LinkedInUser != "" {
		liAdapter, err := adaptSocial.Novo(adaptSocial.ConfigSocial{
			CanalAlvo:      domain.CanalLinkedIn,
			CredencialUser: cfg.Chromedp.LinkedInUser,
			CredencialPass: cfg.Chromedp.LinkedInPass,
			ChromeURL:      cfg.Chromedp.ChromeURL,
			DelayMinMS:     cfg.Chromedp.DelayMinMS,
			DelayMaxMS:     cfg.Chromedp.DelayMaxMS,
		}, logger)
		if err != nil {
			logger.Warn("linkedin indisponível", "erro", err.Error())
		} else {
			mensageiros[domain.CanalLinkedIn] = liAdapter
			logger.Info("adapter linkedin: ativo")
		}
	}

	// Instagram — chromedp
	if cfg.Chromedp.InstaUser != "" {
		igAdapter, err := adaptSocial.Novo(adaptSocial.ConfigSocial{
			CanalAlvo:      domain.CanalInstagram,
			CredencialUser: cfg.Chromedp.InstaUser,
			CredencialPass: cfg.Chromedp.InstaPass,
			ChromeURL:      cfg.Chromedp.ChromeURL,
			DelayMinMS:     cfg.Chromedp.DelayMinMS,
			DelayMaxMS:     cfg.Chromedp.DelayMaxMS,
		}, logger)
		if err != nil {
			logger.Warn("instagram indisponível", "erro", err.Error())
		} else {
			mensageiros[domain.CanalInstagram] = igAdapter
			logger.Info("adapter instagram: ativo")
		}
	}

	// Facebook — chromedp
	if cfg.Chromedp.FBUser != "" {
		fbAdapter, err := adaptSocial.Novo(adaptSocial.ConfigSocial{
			CanalAlvo:      domain.CanalFacebook,
			CredencialUser: cfg.Chromedp.FBUser,
			CredencialPass: cfg.Chromedp.FBPass,
			ChromeURL:      cfg.Chromedp.ChromeURL,
			DelayMinMS:     cfg.Chromedp.DelayMinMS,
			DelayMaxMS:     cfg.Chromedp.DelayMaxMS,
		}, logger)
		if err != nil {
			logger.Warn("facebook indisponível", "erro", err.Error())
		} else {
			mensageiros[domain.CanalFacebook] = fbAdapter
			logger.Info("adapter facebook: ativo")
		}
	}

	// SMS/Notificação — Amazon SNS (closer)
	var notificador ports.NotificacaoPort
	if cfg.Notif.SNSNumeroCloser != "" {
		snsAdapter, err := adaptSMS.Novo(adaptSMS.ConfigSNS{
			AWSRegiao:     cfg.AWS.Regiao,
			NumeroDestino: cfg.Notif.SNSNumeroCloser,
			NomeRemetente: cfg.Notif.SNSNomeDisplay,
		}, logger)
		if err != nil {
			logger.Warn("sns notificação indisponível", "erro", err.Error())
		} else {
			mensageiros[domain.CanalSMS] = snsAdapter
			notificador = snsAdapter
			logger.Info("adapter sms/notificação SNS: ativo")
		}
	}

	logger.Info("adapters carregados", "canais_ativos", len(mensageiros))

	// ============================================================
	// Core: Scoring + Dispatcher
	// ============================================================
	scoring := core.NovoServicoScoring(leadRepo, core.RegrasScore{
		PontosResposta:           cfg.Scoring.PontosResposta,
		PontosClique:             cfg.Scoring.PontosClique,
		PontosInteracaoAdicional: cfg.Scoring.PontosInteracao,
		PontosCampoPreenchido:    cfg.Scoring.PontosCampo,
		ThresholdLeadQuente:      cfg.Scoring.ThresholdLeadQuente,
	}, logger)

	dispatcher := core.NovoDispatcher(
		jobRepo, leadRepo, mensageiros, notificador, scoring,
		core.ConfigDispatcher{
			NumWorkers:         cfg.Workers.Quantidade,
			TamanhoFila:        cfg.Workers.TamanhoFila,
			IntervaloDespacho:  cfg.Workers.IntervaloDespacho,
			MaxJobsPorDespacho: cfg.Workers.MaxJobsPorDespacho,
		},
		logger,
	)

	// ============================================================
	// Contexto com cancelamento para shutdown gracioso
	// ============================================================
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	// ============================================================
	// HTTP API
	// ============================================================
	handler := novoHandler(leadRepo, campanhaRepo, jobRepo, dispatcher, scoring, logger)
	mux := http.NewServeMux()
	registrarRotas(mux, handler)

	servidor := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Servidor.Porta),
		Handler:      mux,
		ReadTimeout:  cfg.Servidor.TimeoutLeitura,
		WriteTimeout: cfg.Servidor.TimeoutEscrita,
		IdleTimeout:  cfg.Servidor.TimeoutIdle,
	}

	// ============================================================
	// Goroutines principais
	// ============================================================
	erros := make(chan error, 2)

	// Dispatcher em background
	go func() {
		logger.Info("dispatcher: iniciando em background")
		if err := dispatcher.Iniciar(ctx); err != nil {
			erros <- fmt.Errorf("dispatcher: %w", err)
		}
	}()

	// Servidor HTTP
	go func() {
		logger.Info("servidor http: iniciando", "addr", servidor.Addr)
		if err := servidor.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			erros <- fmt.Errorf("servidor http: %w", err)
		}
	}()

	// ============================================================
	// Graceful Shutdown — aguarda SIGTERM/SIGINT
	// ============================================================
	sinais := make(chan os.Signal, 1)
	signal.Notify(sinais, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sinais:
		logger.Info("lead-gen-motor: sinal recebido, iniciando shutdown", "sinal", sig.String())

	case err := <-erros:
		logger.Error("lead-gen-motor: erro crítico", "erro", err.Error())
	}

	// Cancela o contexto — para o Dispatcher e Workers
	cancelCtx()

	// Encerra o servidor HTTP com timeout de 30 segundos
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	if err := servidor.Shutdown(shutdownCtx); err != nil {
		logger.Error("lead-gen-motor: falha no shutdown do servidor", "erro", err.Error())
	}

	logger.Info("lead-gen-motor: encerrado com sucesso")
}

// ============================================================
// Handler HTTP
// ============================================================

type handler struct {
	leadRepo    ports.LeadRepositoryPort
	campanhaRepo ports.CampanhaRepositoryPort
	jobRepo     ports.JobRepositoryPort
	dispatcher  *core.Dispatcher
	scoring     *core.ServicoScoring
	logger      *slog.Logger
}

func novoHandler(
	lr ports.LeadRepositoryPort,
	cr ports.CampanhaRepositoryPort,
	jr ports.JobRepositoryPort,
	d *core.Dispatcher,
	s *core.ServicoScoring,
	l *slog.Logger,
) *handler {
	return &handler{
		leadRepo:     lr,
		campanhaRepo: cr,
		jobRepo:      jr,
		dispatcher:   d,
		scoring:      s,
		logger:       l,
	}
}

func registrarRotas(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("GET /api/v1/health",             h.health)
	mux.HandleFunc("GET /api/v1/status",             h.status)

	mux.HandleFunc("POST /api/v1/leads",             h.criarLead)
	mux.HandleFunc("GET /api/v1/leads",              h.listarLeads)
	mux.HandleFunc("GET /api/v1/leads/{id}",         h.buscarLead)
	mux.HandleFunc("PUT /api/v1/leads/{id}",         h.atualizarLead)

	mux.HandleFunc("POST /api/v1/campanhas",                        h.criarCampanha)
	mux.HandleFunc("GET /api/v1/campanhas",                         h.listarCampanhas)
	mux.HandleFunc("GET /api/v1/campanhas/{id}",                    h.buscarCampanha)
	mux.HandleFunc("PUT /api/v1/campanhas/{id}/ativar",             h.ativarCampanha)
	mux.HandleFunc("PUT /api/v1/campanhas/{id}/pausar",             h.pausarCampanha)
	mux.HandleFunc("POST /api/v1/campanhas/{id}/despachar",         h.despacharCampanha)

	mux.HandleFunc("POST /api/v1/webhooks/interacao",               h.webhookInteracao)
}

// ---- Handlers ----

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "ts": time.Now().UTC().Format(time.RFC3339)})
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	responderJSON(w, http.StatusOK, map[string]any{
		"dispatcher": h.dispatcher.Status(),
		"ts":         time.Now().UTC(),
	})
}

func (h *handler) criarLead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Nome            string `json:"nome"`
		Email           string `json:"email"`
		Telefone        string `json:"telefone"`
		LinkedInURL     string `json:"linkedin_url"`
		InstagramHandle string `json:"instagram_handle"`
		FacebookURL     string `json:"facebook_url"`
		Empresa         string `json:"empresa"`
		Cargo           string `json:"cargo"`
		Website         string `json:"website"`
		Localizacao     string `json:"localizacao"`
		Notas           string `json:"notas"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		responderErro(w, http.StatusBadRequest, "JSON inválido: "+err.Error())
		return
	}

	if req.Nome == "" {
		responderErro(w, http.StatusBadRequest, "campo 'nome' é obrigatório")
		return
	}

	lead := domain.Novo(req.Nome, req.Email)
	lead.Telefone = req.Telefone
	lead.LinkedInURL = req.LinkedInURL
	lead.InstagramHandle = req.InstagramHandle
	lead.FacebookURL = req.FacebookURL
	lead.Empresa = req.Empresa
	lead.Cargo = req.Cargo
	lead.Website = req.Website
	lead.Localizacao = req.Localizacao
	lead.Notas = req.Notas

	if !lead.EhValido() {
		responderErro(w, http.StatusBadRequest, "lead precisa de pelo menos um canal de contato")
		return
	}

	if err := h.leadRepo.Salvar(r.Context(), lead); err != nil {
		h.logger.Error("api: falha ao salvar lead", "erro", err.Error())
		responderErro(w, http.StatusInternalServerError, "falha ao salvar lead")
		return
	}

	responderJSON(w, http.StatusCreated, lead)
}

func (h *handler) listarLeads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filtro := ports.FiltroLead{
		Limite:    atoiOuPadrao(q.Get("limite"), 20),
		Offset:    atoiOuPadrao(q.Get("offset"), 0),
		BuscaNome: q.Get("nome"),
	}

	leads, total, err := h.leadRepo.Listar(r.Context(), filtro)
	if err != nil {
		responderErro(w, http.StatusInternalServerError, "falha ao listar leads")
		return
	}

	responderJSON(w, http.StatusOK, map[string]any{
		"dados":  leads,
		"total":  total,
		"limite": filtro.Limite,
		"offset": filtro.Offset,
	})
}

func (h *handler) buscarLead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lead, err := h.leadRepo.BuscarPorID(r.Context(), id)
	if err != nil {
		responderErro(w, http.StatusNotFound, "lead não encontrado")
		return
	}
	responderJSON(w, http.StatusOK, lead)
}

func (h *handler) atualizarLead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lead, err := h.leadRepo.BuscarPorID(r.Context(), id)
	if err != nil {
		responderErro(w, http.StatusNotFound, "lead não encontrado")
		return
	}

	if err := json.NewDecoder(r.Body).Decode(lead); err != nil {
		responderErro(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if err := h.leadRepo.Atualizar(r.Context(), lead); err != nil {
		responderErro(w, http.StatusInternalServerError, "falha ao atualizar lead")
		return
	}

	responderJSON(w, http.StatusOK, lead)
}

func (h *handler) criarCampanha(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Nome            string   `json:"nome"`
		Descricao       string   `json:"descricao"`
		Canais          []string `json:"canais"`
		TemplateAssunto string   `json:"template_assunto"`
		TemplateCorpo   string   `json:"template_corpo"`
		MaxLeads        int      `json:"max_leads"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		responderErro(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if req.Nome == "" || req.TemplateCorpo == "" {
		responderErro(w, http.StatusBadRequest, "nome e template_corpo são obrigatórios")
		return
	}

	canais := make([]domain.Canal, len(req.Canais))
	for i, c := range req.Canais {
		canais[i] = domain.Canal(c)
	}

	campanha := domain.NovaCampanha(req.Nome, req.TemplateCorpo, canais)
	campanha.Descricao = req.Descricao
	campanha.TemplateAssunto = req.TemplateAssunto
	campanha.MaxLeads = req.MaxLeads

	if err := h.campanhaRepo.Salvar(r.Context(), campanha); err != nil {
		h.logger.Error("api: falha ao salvar campanha", "erro", err.Error())
		responderErro(w, http.StatusInternalServerError, "falha ao salvar campanha")
		return
	}

	responderJSON(w, http.StatusCreated, campanha)
}

func (h *handler) listarCampanhas(w http.ResponseWriter, r *http.Request) {
	campanhas, total, err := h.campanhaRepo.Listar(r.Context(), ports.FiltroCampanha{
		Limite: atoiOuPadrao(r.URL.Query().Get("limite"), 20),
		Offset: atoiOuPadrao(r.URL.Query().Get("offset"), 0),
	})
	if err != nil {
		responderErro(w, http.StatusInternalServerError, "falha ao listar campanhas")
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"dados": campanhas, "total": total})
}

func (h *handler) buscarCampanha(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	campanha, err := h.campanhaRepo.BuscarPorID(r.Context(), id)
	if err != nil {
		responderErro(w, http.StatusNotFound, "campanha não encontrada")
		return
	}
	responderJSON(w, http.StatusOK, campanha)
}

func (h *handler) ativarCampanha(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	campanha, err := h.campanhaRepo.BuscarPorID(r.Context(), id)
	if err != nil {
		responderErro(w, http.StatusNotFound, "campanha não encontrada")
		return
	}

	if err := campanha.Ativar(); err != nil {
		responderErro(w, http.StatusConflict, err.Error())
		return
	}

	if err := h.campanhaRepo.Atualizar(r.Context(), campanha); err != nil {
		responderErro(w, http.StatusInternalServerError, "falha ao ativar campanha")
		return
	}

	h.logger.Info("api: campanha ativada", "campanha_id", id, "nome", campanha.Nome)
	responderJSON(w, http.StatusOK, campanha)
}

func (h *handler) pausarCampanha(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	campanha, err := h.campanhaRepo.BuscarPorID(r.Context(), id)
	if err != nil {
		responderErro(w, http.StatusNotFound, "campanha não encontrada")
		return
	}

	if err := campanha.Pausar(); err != nil {
		responderErro(w, http.StatusConflict, err.Error())
		return
	}

	if err := h.campanhaRepo.Atualizar(r.Context(), campanha); err != nil {
		responderErro(w, http.StatusInternalServerError, "falha ao pausar campanha")
		return
	}

	h.logger.Info("api: campanha pausada", "campanha_id", id)
	responderJSON(w, http.StatusOK, campanha)
}

func (h *handler) despacharCampanha(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		LeadsIDs []string `json:"leads_ids"`
		Canal    string   `json:"canal"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		responderErro(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	campanha, err := h.campanhaRepo.BuscarPorID(r.Context(), id)
	if err != nil {
		responderErro(w, http.StatusNotFound, "campanha não encontrada")
		return
	}
	if !campanha.EstaAtiva() {
		responderErro(w, http.StatusConflict, "campanha deve estar ativa para despachar jobs (status atual: "+string(campanha.Status)+")")
		return
	}

	if err := h.jobRepo.CriarJobsParaCampanha(
		r.Context(), id, req.LeadsIDs, domain.Canal(req.Canal),
	); err != nil {
		responderErro(w, http.StatusInternalServerError, "falha ao criar jobs: "+err.Error())
		return
	}

	responderJSON(w, http.StatusAccepted, map[string]any{
		"mensagem":    "jobs criados com sucesso",
		"campanha_id": id,
		"total_leads": len(req.LeadsIDs),
		"canal":       req.Canal,
	})
}

func (h *handler) webhookInteracao(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LeadID string `json:"lead_id"`
		Tipo   string `json:"tipo"`   // "abriu_email", "clicou_link", "respondeu_email"
		Canal  string `json:"canal"`
		Dados  map[string]any `json:"dados"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		responderErro(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	ficouQuente, err := h.scoring.ProcessarInteracao(
		r.Context(),
		req.LeadID,
		req.Tipo,
		domain.Canal(req.Canal),
		req.Dados,
	)
	if err != nil {
		h.logger.Error("api: falha ao processar interação", "erro", err.Error())
		responderErro(w, http.StatusInternalServerError, err.Error())
		return
	}

	responderJSON(w, http.StatusOK, map[string]any{
		"processado":   true,
		"lead_quente":  ficouQuente,
	})
}

// ============================================================
// Helpers
// ============================================================

func responderJSON(w http.ResponseWriter, status int, dados any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(dados)
}

func responderErro(w http.ResponseWriter, status int, mensagem string) {
	responderJSON(w, status, map[string]string{"erro": mensagem})
}

func atoiOuPadrao(s string, padrao int) int {
	if s == "" {
		return padrao
	}
	var n int
	fmt.Sscanf(s, "%d", &n)
	if n <= 0 {
		return padrao
	}
	return n
}

func configurarLogger() *slog.Logger {
	nivel := slog.LevelInfo
	if os.Getenv("APP_AMBIENTE") == "dev" {
		nivel = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{Level: nivel}
	var handler slog.Handler

	if os.Getenv("APP_AMBIENTE") == "prod" {
		// JSON em produção — facilita ingestão no CloudWatch
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		// Texto colorido no desenvolvimento
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
