package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// LimitesCanal define a taxa máxima de envio por canal.
// Crucial para evitar banimentos nas plataformas sociais.
type LimitesCanal struct {
	PorHora   int // máximo de mensagens por hora
	PorDia    int // máximo de mensagens por dia (não implementado aqui — usar DB)
	BurstMax  int // pico máximo instantâneo (token bucket)
}

// LimitesDefault são os limites conservadores por canal.
// Ajuste no limits.yaml conforme sua conta/situação.
var LimitesDefault = map[domain.Canal]LimitesCanal{
	domain.CanalEmail:     {PorHora: 50,  PorDia: 200, BurstMax: 5},
	domain.CanalWhatsApp:  {PorHora: 10,  PorDia: 50,  BurstMax: 2},
	domain.CanalLinkedIn:  {PorHora: 5,   PorDia: 20,  BurstMax: 1},
	domain.CanalInstagram: {PorHora: 5,   PorDia: 20,  BurstMax: 1},
	domain.CanalFacebook:  {PorHora: 5,   PorDia: 20,  BurstMax: 1},
	domain.CanalSMS:       {PorHora: 20,  PorDia: 100, BurstMax: 3},
}

// Worker consome jobs da fila e processa com rate limiting por canal.
type Worker struct {
	id          int
	filaJobs    <-chan *domain.Job
	jobRepo     ports.JobRepositoryPort
	leadRepo    ports.LeadRepositoryPort
	mensageiros map[domain.Canal]ports.MessengerPort
	notificador ports.NotificacaoPort
	scoring     *ServicoScoring
	limitadores map[domain.Canal]*rate.Limiter
	logger      *slog.Logger
	ativo       atomic.Bool
	totalEnviados atomic.Int64
	totalFalhas   atomic.Int64
}

// NovoWorker cria um Worker com limitadores de taxa por canal.
func NovoWorker(
	id int,
	filaJobs <-chan *domain.Job,
	jobRepo ports.JobRepositoryPort,
	leadRepo ports.LeadRepositoryPort,
	mensageiros map[domain.Canal]ports.MessengerPort,
	notificador ports.NotificacaoPort,
	scoring *ServicoScoring,
	logger *slog.Logger,
) *Worker {
	limitadores := make(map[domain.Canal]*rate.Limiter, len(LimitesDefault))
	for canal, limites := range LimitesDefault {
		// Converte PorHora em taxa por segundo para o rate.Limiter
		taxaPorSegundo := rate.Limit(float64(limites.PorHora) / 3600.0)
		limitadores[canal] = rate.NewLimiter(taxaPorSegundo, limites.BurstMax)
	}

	return &Worker{
		id:          id,
		filaJobs:    filaJobs,
		jobRepo:     jobRepo,
		leadRepo:    leadRepo,
		mensageiros: mensageiros,
		notificador: notificador,
		scoring:     scoring,
		limitadores: limitadores,
		logger:      logger.With("worker_id", id),
	}
}

// Rodar é o loop principal do Worker — consome da fila até o contexto cancelar.
func (w *Worker) Rodar(ctx context.Context) {
	w.ativo.Store(true)
	defer w.ativo.Store(false)

	w.logger.Info("worker: iniciado")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker: finalizando",
				"total_enviados", w.totalEnviados.Load(),
				"total_falhas", w.totalFalhas.Load(),
			)
			return

		case job, ok := <-w.filaJobs:
			if !ok {
				// Channel fechado pelo Dispatcher no shutdown
				w.logger.Info("worker: fila fechada, finalizando")
				return
			}
			w.processarJob(ctx, job)
		}
	}
}

// processarJob executa um job com rate limiting, retry e registro de resultado.
func (w *Worker) processarJob(ctx context.Context, job *domain.Job) {
	logger := w.logger.With(
		"job_id", job.ID,
		"lead_id", job.Lead.ID,
		"canal", job.Canal,
		"tentativa", job.Tentativas+1,
		"max_tentativas", job.MaxTentativas,
	)

	// Verifica se o messenger para este canal existe
	messenger, ok := w.mensageiros[job.Canal]
	if !ok || !messenger.Disponivel() {
		logger.Warn("worker: canal não disponível ou não configurado")
		w.marcarFalha(ctx, job, fmt.Errorf("canal '%s' não disponível", job.Canal), false)
		return
	}

	// Aguarda o rate limiter permitir o envio
	// Wait bloqueia até o token estar disponível — respeita ctx para shutdown
	limitador, temLimitador := w.limitadores[job.Canal]
	if temLimitador {
		inicio := time.Now()
		if err := limitador.Wait(ctx); err != nil {
			if ctx.Err() != nil {
				return // shutdown limpo
			}
			logger.Error("worker: rate limiter erro", "erro", err.Error())
			return
		}
		esperaMS := time.Since(inicio).Milliseconds()
		if esperaMS > 100 {
			logger.Debug("worker: aguardou rate limiter", "espera_ms", esperaMS)
		}
	}

	// Renderiza a mensagem com os dados do lead
	assunto, corpo := job.Campanha.RenderizarMensagem(job.Lead)

	msgEnvio := ports.MensagemEnvio{
		Lead:    job.Lead,
		Assunto: assunto,
		Corpo:   corpo,
		Canal:   job.Canal,
	}

	// Executa o envio
	resultado := messenger.Enviar(ctx, msgEnvio)

	if resultado.Sucesso {
		w.totalEnviados.Add(1)
		logger.Info("worker: mensagem enviada com sucesso",
			"id_externo", resultado.IDExterno,
		)

		// Atualiza job e lead
		w.marcarSucesso(ctx, job, resultado.IDExterno)
		w.atualizarLeadAposEnvio(ctx, job.Lead, job.Canal)
		w.avaliarScoreENotificar(ctx, job)

	} else {
		w.totalFalhas.Add(1)
		logger.Warn("worker: falha no envio",
			"erro", resultado.Erro.Error(),
			"deve_retentar", resultado.DeveRetentar,
		)
		w.marcarFalha(ctx, job, resultado.Erro, resultado.DeveRetentar && job.PodeRetentar())
	}
}

// marcarSucesso atualiza o job como enviado no banco.
func (w *Worker) marcarSucesso(ctx context.Context, job *domain.Job, idExterno string) {
	if err := w.jobRepo.MarcarEnviado(ctx, job.ID, idExterno); err != nil {
		w.logger.Error("worker: falha ao marcar job como enviado",
			"job_id", job.ID,
			"erro", err.Error(),
		)
	}
}

// marcarFalha atualiza o job como falho, com ou sem possibilidade de retry.
func (w *Worker) marcarFalha(ctx context.Context, job *domain.Job, erro error, podeRetentar bool) {
	erroStr := ""
	if erro != nil {
		erroStr = erro.Error()
	}
	if err := w.jobRepo.MarcarFalhado(ctx, job.ID, erroStr, podeRetentar); err != nil {
		w.logger.Error("worker: falha ao marcar job como falhado",
			"job_id", job.ID,
			"erro", err.Error(),
		)
	}
}

// atualizarLeadAposEnvio registra o contato e incrementa pontuação por campo.
func (w *Worker) atualizarLeadAposEnvio(ctx context.Context, lead *domain.Lead, canal domain.Canal) {
	lead.RegistrarContato()

	// Pontuação pela completude do perfil (executa uma vez por lead, não por envio)
	pontosCompletude := lead.CamposPreenchidos() * domain.PontuacaoCampoPreenchido
	if pontosCompletude > 0 && lead.Pontuacao == 0 {
		lead.AdicionarPontos(pontosCompletude)
	}

	lead.CanalPreferido = canal

	if err := w.leadRepo.Atualizar(ctx, lead); err != nil {
		w.logger.Error("worker: falha ao atualizar lead",
			"lead_id", lead.ID,
			"erro", err.Error(),
		)
	}
}

// avaliarScoreENotificar calcula o score e notifica o closer se o lead ficou quente.
func (w *Worker) avaliarScoreENotificar(ctx context.Context, job *domain.Job) {
	ficouQuente, err := w.scoring.RecalcularScore(ctx, job.Lead)
	if err != nil {
		w.logger.Error("worker: falha no recálculo de score",
			"lead_id", job.Lead.ID,
			"erro", err.Error(),
		)
		return
	}

	if ficouQuente && w.notificador != nil {
		if err := w.notificador.NotificarLeadQuente(ctx, job.Lead, job.Campanha); err != nil {
			w.logger.Error("worker: falha ao notificar lead quente",
				"lead_id", job.Lead.ID,
				"erro", err.Error(),
			)
		}
	}
}

// EstaAtivo retorna true se o Worker está processando ativamente.
func (w *Worker) EstaAtivo() bool {
	return w.ativo.Load()
}

// Metricas retorna contadores de envio deste Worker.
func (w *Worker) Metricas() (enviados, falhas int64) {
	return w.totalEnviados.Load(), w.totalFalhas.Load()
}
