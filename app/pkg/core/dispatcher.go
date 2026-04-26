// Package core contém a lógica de orquestração central da aplicação.
// O Dispatcher e os Workers implementam o padrão produtor-consumidor com
// rate limiting por canal para evitar banimentos nas plataformas sociais.
package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// ConfigDispatcher configura o comportamento do motor de envio.
type ConfigDispatcher struct {
	NumWorkers            int
	TamanhoFila           int           // buffer do channel de jobs
	IntervaloDespacho     time.Duration // frequência de busca de jobs pendentes no banco
	MaxJobsPorDespacho    int           // máximo de jobs buscados por ciclo
}

// Dispatcher é o produtor da fila — busca jobs pendentes no banco
// e os coloca no channel para os Workers consumirem.
type Dispatcher struct {
	filaJobs    chan *domain.Job
	jobRepo     ports.JobRepositoryPort
	leadRepo    ports.LeadRepositoryPort
	mensageiros map[domain.Canal]ports.MessengerPort
	notificador ports.NotificacaoPort
	scoring     *ServicoScoring
	workers     []*Worker
	cfg         ConfigDispatcher
	logger      *slog.Logger
	wg          sync.WaitGroup
	mu          sync.RWMutex
	rodando     bool
}

// NovoDispatcher cria e configura o Dispatcher com todos os adapters injetados.
func NovoDispatcher(
	jobRepo ports.JobRepositoryPort,
	leadRepo ports.LeadRepositoryPort,
	mensageiros map[domain.Canal]ports.MessengerPort,
	notificador ports.NotificacaoPort,
	scoring *ServicoScoring,
	cfg ConfigDispatcher,
	logger *slog.Logger,
) *Dispatcher {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 5
	}
	if cfg.TamanhoFila <= 0 {
		cfg.TamanhoFila = 100
	}
	if cfg.IntervaloDespacho <= 0 {
		cfg.IntervaloDespacho = 30 * time.Second
	}
	if cfg.MaxJobsPorDespacho <= 0 {
		cfg.MaxJobsPorDespacho = 50
	}

	return &Dispatcher{
		filaJobs:    make(chan *domain.Job, cfg.TamanhoFila),
		jobRepo:     jobRepo,
		leadRepo:    leadRepo,
		mensageiros: mensageiros,
		notificador: notificador,
		scoring:     scoring,
		cfg:         cfg,
		logger:      logger,
	}
}

// Iniciar cria os Workers e começa o loop de despacho.
// Bloqueante — deve ser chamado em uma goroutine.
func (d *Dispatcher) Iniciar(ctx context.Context) error {
	d.mu.Lock()
	if d.rodando {
		d.mu.Unlock()
		return fmt.Errorf("dispatcher: já está rodando")
	}
	d.rodando = true
	d.mu.Unlock()

	d.logger.Info("dispatcher: iniciando",
		"workers", d.cfg.NumWorkers,
		"fila_tamanho", d.cfg.TamanhoFila,
		"intervalo", d.cfg.IntervaloDespacho,
	)

	// Inicia os Workers
	d.workers = make([]*Worker, d.cfg.NumWorkers)
	for i := 0; i < d.cfg.NumWorkers; i++ {
		worker := NovoWorker(
			i+1,
			d.filaJobs,
			d.jobRepo,
			d.leadRepo,
			d.mensageiros,
			d.notificador,
			d.scoring,
			d.logger,
		)
		d.workers[i] = worker
		d.wg.Add(1)
		go func(w *Worker) {
			defer d.wg.Done()
			w.Rodar(ctx)
		}(worker)
	}

	// Loop principal de despacho
	ticker := time.NewTicker(d.cfg.IntervaloDespacho)
	defer ticker.Stop()

	// Executa imediatamente na primeira vez
	d.despacharJobsPendentes(ctx)

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("dispatcher: contexto cancelado, aguardando workers finalizarem...")
			close(d.filaJobs)
			d.wg.Wait()
			d.logger.Info("dispatcher: todos os workers finalizados")
			return nil

		case <-ticker.C:
			d.despacharJobsPendentes(ctx)
		}
	}
}

// despacharJobsPendentes busca jobs no banco e os envia para o channel.
// Se o channel estiver cheio (backpressure), jobs são ignorados neste ciclo.
func (d *Dispatcher) despacharJobsPendentes(ctx context.Context) {
	jobs, err := d.jobRepo.BuscarPendentes(ctx, d.cfg.MaxJobsPorDespacho)
	if err != nil {
		d.logger.Error("dispatcher: falha ao buscar jobs pendentes", "erro", err.Error())
		return
	}

	if len(jobs) == 0 {
		return
	}

	despachados := 0
	ignorados := 0

	for _, job := range jobs {
		// Tenta enviar para o channel sem bloquear (não-bloqueante)
		select {
		case d.filaJobs <- job:
			// Marca como "processando" imediatamente para evitar re-despacho duplicado
			if err := d.jobRepo.MarcarProcessando(ctx, job.ID); err != nil {
				d.logger.Warn("dispatcher: falha ao marcar job como processando",
					"job_id", job.ID,
					"erro", err.Error(),
				)
			}
			despachados++

		default:
			// Channel cheio — backpressure, job fica pendente para próximo ciclo
			ignorados++
		}
	}

	if despachados > 0 || ignorados > 0 {
		d.logger.Info("dispatcher: ciclo de despacho",
			"encontrados", len(jobs),
			"despachados", despachados,
			"ignorados_fila_cheia", ignorados,
		)
	}
}

// EnfileirarJob permite inserir um job diretamente (usado pela API HTTP).
func (d *Dispatcher) EnfileirarJob(ctx context.Context, job *domain.Job) error {
	select {
	case d.filaJobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("dispatcher: fila cheia, tente novamente em instantes")
	}
}

// Status retorna métricas do Dispatcher para o health check.
func (d *Dispatcher) Status() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	workersAtivos := 0
	for _, w := range d.workers {
		if w.EstaAtivo() {
			workersAtivos++
		}
	}

	return map[string]any{
		"rodando":          d.rodando,
		"workers_total":    d.cfg.NumWorkers,
		"workers_ativos":   workersAtivos,
		"fila_tamanho":     len(d.filaJobs),
		"fila_capacidade":  d.cfg.TamanhoFila,
		"fila_ocupacao_pct": int(float64(len(d.filaJobs)) / float64(d.cfg.TamanhoFila) * 100),
	}
}
