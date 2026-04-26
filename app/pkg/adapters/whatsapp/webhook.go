// Package whatsapp implementa o adapter para WhatsApp Business Cloud API (Meta).
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// ConfigWhatsApp contém as credenciais da Meta Business Cloud API.
type ConfigWhatsApp struct {
	APIURL      string // https://graph.facebook.com/v18.0
	Token       string // Token de acesso permanente
	PhoneNumberID string
}

// AdaptadorWhatsApp implementa MessengerPort via WhatsApp Business API.
type AdaptadorWhatsApp struct {
	cfg        ConfigWhatsApp
	httpCliente *http.Client
	logger     *slog.Logger
}

// Novo cria uma instância do adaptador WhatsApp.
func Novo(cfg ConfigWhatsApp, logger *slog.Logger) (*AdaptadorWhatsApp, error) {
	if cfg.Token == "" || cfg.PhoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp: token e PhoneNumberID são obrigatórios")
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://graph.facebook.com/v18.0"
	}

	return &AdaptadorWhatsApp{
		cfg: cfg,
		httpCliente: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}, nil
}

// Canal retorna o identificador do canal.
func (a *AdaptadorWhatsApp) Canal() domain.Canal {
	return domain.CanalWhatsApp
}

// Disponivel verifica se as credenciais estão configuradas.
func (a *AdaptadorWhatsApp) Disponivel() bool {
	return a.cfg.Token != "" && a.cfg.PhoneNumberID != ""
}

// payloadMensagemTexto representa o payload JSON para mensagem de texto.
type payloadMensagemTexto struct {
	MessagingProduct string  `json:"messaging_product"`
	RecipientType    string  `json:"recipient_type"`
	To               string  `json:"to"`
	Type             string  `json:"type"`
	Text             textoWA `json:"text"`
}

type textoWA struct {
	PreviewURL bool   `json:"preview_url"`
	Body       string `json:"body"`
}

type respostaWA struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// Enviar despacha uma mensagem de texto pelo WhatsApp Business API.
// IMPORTANTE: O lead DEVE ter consentimento LGPD/GDPR para receber mensagens.
// A API exige que o usuário tenha enviado uma mensagem para o número nos últimos 24h
// OU que seja uma mensagem de template aprovado.
func (a *AdaptadorWhatsApp) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	telefone := normalizarTelefone(msg.Lead.Telefone)
	if telefone == "" {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("whatsapp: lead '%s' não tem telefone válido", msg.Lead.Nome),
			DeveRetentar: false,
		}
	}

	// Limita a 1024 chars — limite WhatsApp para mensagens de texto
	corpo := msg.Corpo
	if len(corpo) > 1024 {
		corpo = corpo[:1021] + "..."
	}

	payload := payloadMensagemTexto{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               telefone,
		Type:             "text",
		Text: textoWA{
			PreviewURL: false,
			Body:       corpo,
		},
	}

	corpo_json, err := json.Marshal(payload)
	if err != nil {
		return ports.ResultadoEnvio{
			Sucesso: false,
			Erro:    fmt.Errorf("whatsapp: falha ao serializar payload: %w", err),
		}
	}

	url := fmt.Sprintf("%s/%s/messages", a.cfg.APIURL, a.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(corpo_json))
	if err != nil {
		return ports.ResultadoEnvio{Sucesso: false, Erro: err, DeveRetentar: true}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)

	resp, err := a.httpCliente.Do(req)
	if err != nil {
		a.logger.Error("whatsapp: falha na requisição HTTP",
			"lead_id", msg.Lead.ID,
			"telefone", telefone,
			"erro", err.Error(),
		)
		return ports.ResultadoEnvio{Sucesso: false, Erro: err, DeveRetentar: true}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var resposta respostaWA
	if err := json.Unmarshal(body, &resposta); err != nil {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("whatsapp: resposta inválida da API: %s", string(body)),
			DeveRetentar: true,
		}
	}

	if resposta.Error != nil {
		// Código 131030 = número não existe, não retentar
		deveRetentar := resposta.Error.Code != 131030 && resposta.Error.Code != 131031
		a.logger.Error("whatsapp: erro da API",
			"lead_id", msg.Lead.ID,
			"codigo", resposta.Error.Code,
			"mensagem", resposta.Error.Message,
		)
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("whatsapp API erro %d: %s", resposta.Error.Code, resposta.Error.Message),
			DeveRetentar: deveRetentar,
		}
	}

	idMensagem := ""
	if len(resposta.Messages) > 0 {
		idMensagem = resposta.Messages[0].ID
	}

	a.logger.Info("whatsapp: mensagem enviada",
		"lead_id", msg.Lead.ID,
		"telefone", telefone,
		"message_id", idMensagem,
	)

	return ports.ResultadoEnvio{
		Sucesso:   true,
		IDExterno: idMensagem,
	}
}

// normalizarTelefone converte para formato E.164 sem o '+'.
// A API Meta aceita: 5511999999999 (sem + e sem espaços)
func normalizarTelefone(telefone string) string {
	numeros := make([]byte, 0, len(telefone))
	for i := 0; i < len(telefone); i++ {
		if telefone[i] >= '0' && telefone[i] <= '9' {
			numeros = append(numeros, telefone[i])
		}
	}
	if len(numeros) < 10 {
		return ""
	}
	// Adiciona DDI Brasil se não tiver
	if len(numeros) == 11 && numeros[0] != '5' {
		numeros = append([]byte("55"), numeros...)
	}
	return string(numeros)
}
