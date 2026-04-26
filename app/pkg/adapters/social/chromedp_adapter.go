// Package social implementa automação de redes sociais via headless browser (chromedp).
// ATENÇÃO: Automação de redes sociais viola os ToS das plataformas.
// Use exclusivamente para fins legais: próprios perfis, conteúdo autorizado,
// testes automatizados, ou cenários onde você tem permissão explícita.
package social

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// ConfigSocial contém configurações para cada rede social.
type ConfigSocial struct {
	CanalAlvo      domain.Canal
	CredencialUser string
	CredencialPass string
	ChromeURL      string // ws://chromedp:9222 (container Docker)
	DelayMinMS     int    // delay mínimo entre ações (anti-detecção)
	DelayMaxMS     int    // delay máximo entre ações
}

// AdaptadorSocial implementa MessengerPort para redes sociais via chromedp.
type AdaptadorSocial struct {
	cfg    ConfigSocial
	logger *slog.Logger
}

// Novo cria um adaptador para a rede social especificada.
func Novo(cfg ConfigSocial, logger *slog.Logger) (*AdaptadorSocial, error) {
	if cfg.CanalAlvo == "" {
		return nil, fmt.Errorf("social: CanalAlvo é obrigatório")
	}
	if cfg.DelayMinMS == 0 {
		cfg.DelayMinMS = 500
	}
	if cfg.DelayMaxMS == 0 {
		cfg.DelayMaxMS = 2000
	}
	if cfg.ChromeURL == "" {
		cfg.ChromeURL = "ws://chromedp:9222"
	}

	return &AdaptadorSocial{cfg: cfg, logger: logger}, nil
}

func (a *AdaptadorSocial) Canal() domain.Canal { return a.cfg.CanalAlvo }

func (a *AdaptadorSocial) Disponivel() bool {
	return a.cfg.CredencialUser != "" && a.cfg.CredencialPass != ""
}

// Enviar executa automação de envio de mensagem na rede social configurada.
func (a *AdaptadorSocial) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	switch a.cfg.CanalAlvo {
	case domain.CanalLinkedIn:
		return a.enviarLinkedIn(ctx, msg)
	case domain.CanalInstagram:
		return a.enviarInstagram(ctx, msg)
	case domain.CanalFacebook:
		return a.enviarFacebook(ctx, msg)
	default:
		return ports.ResultadoEnvio{
			Sucesso: false,
			Erro:    fmt.Errorf("social: canal '%s' não suportado", a.cfg.CanalAlvo),
		}
	}
}

// criarContextoChrome cria um contexto chromedp conectado ao container Chrome remoto.
func (a *AdaptadorSocial) criarContextoChrome(ctx context.Context) (context.Context, context.CancelFunc, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		// User agent realista para reduzir detecção
		chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	var allocCtx context.Context
	var cancelAlloc context.CancelFunc

	if a.cfg.ChromeURL != "" {
		// Conecta ao Chrome remoto (container Docker) — preferido em produção
		allocCtx, cancelAlloc = chromedp.NewRemoteAllocator(ctx, a.cfg.ChromeURL)
	} else {
		// Chrome local — fallback para desenvolvimento
		allocCtx, cancelAlloc = chromedp.NewExecAllocator(ctx, opts...)
	}

	chromeCtx, cancelChrome := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(func(format string, args ...any) {
			a.logger.Debug("chromedp", "msg", fmt.Sprintf(format, args...))
		}),
	)

	cancelCombinado := func() {
		cancelChrome()
		cancelAlloc()
	}

	return chromeCtx, cancelCombinado, nil
}

// pausaHumana adiciona delay aleatório para simular comportamento humano.
func (a *AdaptadorSocial) pausaHumana() {
	delay := a.cfg.DelayMinMS + rand.Intn(a.cfg.DelayMaxMS-a.cfg.DelayMinMS)
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

// ============================================================
// LinkedIn
// ============================================================

func (a *AdaptadorSocial) enviarLinkedIn(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	if msg.Lead.LinkedInURL == "" {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("linkedin: lead '%s' não tem URL do LinkedIn", msg.Lead.Nome),
			DeveRetentar: false,
		}
	}

	chromeCtx, cancel, err := a.criarContextoChrome(ctx)
	if err != nil {
		return ports.ResultadoEnvio{Sucesso: false, Erro: err, DeveRetentar: true}
	}
	defer cancel()

	// Timeout total para a operação no LinkedIn
	chromeCtx, cancelTimeout := context.WithTimeout(chromeCtx, 90*time.Second)
	defer cancelTimeout()

	var erroAutomacao error

	erroAutomacao = chromedp.Run(chromeCtx,
		// 1. Acessa página de login
		chromedp.Navigate("https://www.linkedin.com/login"),
		chromedp.WaitVisible(`#username`, chromedp.ByID),
	)
	if erroAutomacao != nil {
		return a.erroChrome("linkedin: falha ao acessar login", erroAutomacao)
	}

	a.pausaHumana()

	// 2. Preenche credenciais
	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.SendKeys(`#username`, a.cfg.CredencialUser, chromedp.ByID),
		chromedp.SendKeys(`#password`, a.cfg.CredencialPass, chromedp.ByID),
	)
	if erroAutomacao != nil {
		return a.erroChrome("linkedin: falha ao preencher credenciais", erroAutomacao)
	}

	a.pausaHumana()

	// 3. Clica em login e aguarda redirecionamento
	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.global-nav`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("linkedin: falha no login (captcha ou credenciais inválidas)", erroAutomacao)
	}

	a.pausaHumana()

	// 4. Acessa perfil do lead
	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.Navigate(msg.Lead.LinkedInURL),
		chromedp.WaitVisible(`.pv-top-card`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("linkedin: falha ao acessar perfil do lead", erroAutomacao)
	}

	a.pausaHumana()

	// 5. Clica no botão "Conectar" ou "Enviar mensagem"
	var botaoConectar bool
	chromedp.Run(chromeCtx, //nolint — verifica existência sem falhar
		chromedp.EvaluateAsDevTools(
			`document.querySelector('button[aria-label*="Connect"]') !== null`,
			&botaoConectar,
		),
	)

	if botaoConectar {
		// Envia convite de conexão com nota personalizada
		erroAutomacao = a.enviarConexaoLinkedIn(chromeCtx, msg.Corpo)
	} else {
		// Já conectado — envia mensagem direta
		erroAutomacao = a.enviarMensagemDiretaLinkedIn(chromeCtx, msg.Corpo)
	}

	if erroAutomacao != nil {
		return a.erroChrome("linkedin: falha ao enviar mensagem/conexão", erroAutomacao)
	}

	a.logger.Info("linkedin: ação executada com sucesso",
		"lead_id", msg.Lead.ID,
		"lead_nome", msg.Lead.Nome,
		"linkedin_url", msg.Lead.LinkedInURL,
	)

	return ports.ResultadoEnvio{Sucesso: true, IDExterno: "linkedin:" + msg.Lead.LinkedInURL}
}

func (a *AdaptadorSocial) enviarConexaoLinkedIn(ctx context.Context, nota string) error {
	// Clica em conectar
	if err := chromedp.Run(ctx,
		chromedp.Click(`button[aria-label*="Connect"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.artdeco-modal`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("falha ao clicar em conectar: %w", err)
	}

	a.pausaHumana()

	// Tenta adicionar nota (botão "Add a note")
	var temBotaoNota bool
	chromedp.Run(ctx, //nolint
		chromedp.EvaluateAsDevTools(
			`document.querySelector('button[aria-label="Add a note"]') !== null`,
			&temBotaoNota,
		),
	)

	if temBotaoNota && nota != "" {
		// Limita nota a 300 chars (limite LinkedIn)
		if len(nota) > 300 {
			nota = nota[:297] + "..."
		}
		if err := chromedp.Run(ctx,
			chromedp.Click(`button[aria-label="Add a note"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`textarea[name="message"]`, chromedp.ByQuery),
			chromedp.SendKeys(`textarea[name="message"]`, nota, chromedp.ByQuery),
		); err != nil {
			return fmt.Errorf("falha ao adicionar nota: %w", err)
		}
		a.pausaHumana()
	}

	// Envia o convite
	return chromedp.Run(ctx,
		chromedp.Click(`button[aria-label="Send now"]`, chromedp.ByQuery),
	)
}

func (a *AdaptadorSocial) enviarMensagemDiretaLinkedIn(ctx context.Context, mensagem string) error {
	if len(mensagem) > 8000 {
		mensagem = mensagem[:7997] + "..."
	}

	return chromedp.Run(ctx,
		chromedp.Click(`button[aria-label*="Message"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.msg-form__contenteditable`, chromedp.ByQuery),
		chromedp.SendKeys(`.msg-form__contenteditable`, mensagem, chromedp.ByQuery),
		chromedp.Submit(`.msg-form__contenteditable`, chromedp.ByQuery),
	)
}

// ============================================================
// Instagram
// ============================================================

func (a *AdaptadorSocial) enviarInstagram(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	if msg.Lead.InstagramHandle == "" {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("instagram: lead '%s' não tem handle do Instagram", msg.Lead.Nome),
			DeveRetentar: false,
		}
	}

	chromeCtx, cancel, err := a.criarContextoChrome(ctx)
	if err != nil {
		return ports.ResultadoEnvio{Sucesso: false, Erro: err, DeveRetentar: true}
	}
	defer cancel()

	chromeCtx, cancelTimeout := context.WithTimeout(chromeCtx, 90*time.Second)
	defer cancelTimeout()

	handle := msg.Lead.InstagramHandle
	if len(handle) > 0 && handle[0] == '@' {
		handle = handle[1:]
	}

	var erroAutomacao error

	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.Navigate("https://www.instagram.com/accounts/login/"),
		chromedp.WaitVisible(`input[name="username"]`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("instagram: falha ao acessar login", erroAutomacao)
	}

	a.pausaHumana()

	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.SendKeys(`input[name="username"]`, a.cfg.CredencialUser, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="password"]`, a.cfg.CredencialPass, chromedp.ByQuery),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`svg[aria-label="Home"]`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("instagram: falha no login", erroAutomacao)
	}

	a.pausaHumana()

	// Acessa perfil do lead e clica em "Message"
	profileURL := fmt.Sprintf("https://www.instagram.com/%s/", handle)
	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.Navigate(profileURL),
		chromedp.WaitVisible(`header`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("instagram: falha ao acessar perfil", erroAutomacao)
	}

	a.pausaHumana()

	corpo := msg.Corpo
	if len(corpo) > 1000 {
		corpo = corpo[:997] + "..."
	}

	// Clica em "Message" via JavaScript (seletor :has-text() não é CSS padrão)
	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.ActionFunc(func(evalCtx context.Context) error {
			var clicou bool
			return chromedp.Evaluate(`
				(() => {
					const candidates = document.querySelectorAll('div[role="button"], button');
					for (const el of candidates) {
						if (el.innerText && el.innerText.trim() === 'Message') {
							el.click();
							return true;
						}
					}
					return false;
				})()
			`, &clicou).Do(evalCtx)
		}),
		chromedp.WaitVisible(`div[aria-label="Message"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[aria-label="Message"]`, corpo, chromedp.ByQuery),
		chromedp.Submit(`div[aria-label="Message"]`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("instagram: falha ao enviar DM", erroAutomacao)
	}

	a.logger.Info("instagram: DM enviada",
		"lead_id", msg.Lead.ID,
		"handle", handle,
	)

	return ports.ResultadoEnvio{Sucesso: true, IDExterno: "instagram:@" + handle}
}

// ============================================================
// Facebook
// ============================================================

func (a *AdaptadorSocial) enviarFacebook(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	if msg.Lead.FacebookURL == "" {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("facebook: lead '%s' não tem URL do Facebook", msg.Lead.Nome),
			DeveRetentar: false,
		}
	}

	chromeCtx, cancel, err := a.criarContextoChrome(ctx)
	if err != nil {
		return ports.ResultadoEnvio{Sucesso: false, Erro: err, DeveRetentar: true}
	}
	defer cancel()

	chromeCtx, cancelTimeout := context.WithTimeout(chromeCtx, 90*time.Second)
	defer cancelTimeout()

	erroAutomacao := chromedp.Run(chromeCtx,
		chromedp.Navigate("https://www.facebook.com/login"),
		chromedp.WaitVisible(`#email`, chromedp.ByID),
		chromedp.SendKeys(`#email`, a.cfg.CredencialUser, chromedp.ByID),
		chromedp.SendKeys(`#pass`, a.cfg.CredencialPass, chromedp.ByID),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[aria-label="Facebook"]`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("facebook: falha no login", erroAutomacao)
	}

	a.pausaHumana()

	corpo := msg.Corpo
	if len(corpo) > 8000 {
		corpo = corpo[:7997] + "..."
	}

	erroAutomacao = chromedp.Run(chromeCtx,
		chromedp.Navigate(msg.Lead.FacebookURL),
		chromedp.WaitVisible(`[aria-label="Message"]`, chromedp.ByQuery),
		chromedp.Click(`[aria-label="Message"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`div[contenteditable="true"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[contenteditable="true"]`, corpo, chromedp.ByQuery),
		chromedp.Submit(`div[contenteditable="true"]`, chromedp.ByQuery),
	)
	if erroAutomacao != nil {
		return a.erroChrome("facebook: falha ao enviar mensagem", erroAutomacao)
	}

	return ports.ResultadoEnvio{Sucesso: true, IDExterno: "facebook:" + msg.Lead.FacebookURL}
}

func (a *AdaptadorSocial) erroChrome(contexto string, err error) ports.ResultadoEnvio {
	a.logger.Error("social: erro chromedp",
		"contexto", contexto,
		"canal", a.cfg.CanalAlvo,
		"erro", err.Error(),
	)
	return ports.ResultadoEnvio{
		Sucesso:      false,
		Erro:         fmt.Errorf("%s: %w", contexto, err),
		DeveRetentar: true,
	}
}
