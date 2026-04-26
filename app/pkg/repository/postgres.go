// Package repository implementa os adapters de persistência usando PostgreSQL.
package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq" // driver PostgreSQL

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// DB encapsula a conexão com o PostgreSQL e implementa todos os repositórios.
type DB struct {
	conn *sql.DB
}

// ConfigDB contém os parâmetros de conexão com o PostgreSQL.
type ConfigDB struct {
	Host     string
	Porta    int
	Nome     string
	Usuario  string
	Senha    string
	SSLMode  string
	MaxConns int
	MaxIdle  int
}

// Novo cria e valida a conexão com o banco de dados.
func Novo(cfg ConfigDB) (*DB, error) {
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 10
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = 5
	}

	dsn := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		cfg.Host, cfg.Porta, cfg.Nome, cfg.Usuario, cfg.Senha, cfg.SSLMode,
	)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: falha ao abrir conexão: %w", err)
	}

	conn.SetMaxOpenConns(cfg.MaxConns)
	conn.SetMaxIdleConns(cfg.MaxIdle)
	conn.SetConnMaxLifetime(5 * time.Minute)
	conn.SetConnMaxIdleTime(2 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("postgres: falha ao conectar (ping): %w", err)
	}

	return &DB{conn: conn}, nil
}

// Fechar encerra a conexão com o banco.
func (db *DB) Fechar() error {
	return db.conn.Close()
}

// ============================================================
// LeadRepository
// ============================================================

// Compiletime check — garante que *DB implementa a interface.
var _ ports.LeadRepositoryPort = (*LeadRepository)(nil)

// LeadRepository é o adapter PostgreSQL para leads.
type LeadRepository struct{ db *DB }

// NovoLeadRepository cria o repositório de leads.
func NovoLeadRepository(db *DB) *LeadRepository { return &LeadRepository{db: db} }

// Salvar insere um novo lead no banco.
func (r *LeadRepository) Salvar(ctx context.Context, lead *domain.Lead) error {
	query := `
		INSERT INTO leads (
			nome, email, telefone, linkedin_url, instagram_handle,
			facebook_url, empresa, cargo, website, localizacao,
			status, pontuacao, canal_preferido, notas, tags
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
		) RETURNING id, criado_em, atualizado_em`

	var canalPreferido *string
	if lead.CanalPreferido != "" {
		s := string(lead.CanalPreferido)
		canalPreferido = &s
	}

	return r.db.conn.QueryRowContext(ctx, query,
		lead.Nome, toNullStr(lead.Email), toNullStr(lead.Telefone),
		toNullStr(lead.LinkedInURL), toNullStr(lead.InstagramHandle),
		toNullStr(lead.FacebookURL), toNullStr(lead.Empresa),
		toNullStr(lead.Cargo), toNullStr(lead.Website), toNullStr(lead.Localizacao),
		string(lead.Status), lead.Pontuacao, canalPreferido,
		toNullStr(lead.Notas), pqArray(lead.Tags),
	).Scan(&lead.ID, &lead.CriadoEm, &lead.AtualizadoEm)
}

// BuscarPorID encontra um lead pelo UUID.
func (r *LeadRepository) BuscarPorID(ctx context.Context, id string) (*domain.Lead, error) {
	query := `
		SELECT id, nome, email, telefone, linkedin_url, instagram_handle,
		       facebook_url, empresa, cargo, website, localizacao,
		       status, pontuacao, canal_preferido, notas, tags,
		       criado_em, atualizado_em, ultimo_contato_em
		FROM leads WHERE id = $1`

	return r.scanLead(r.db.conn.QueryRowContext(ctx, query, id))
}

// BuscarPorEmail encontra um lead pelo email (índice único).
func (r *LeadRepository) BuscarPorEmail(ctx context.Context, email string) (*domain.Lead, error) {
	query := `
		SELECT id, nome, email, telefone, linkedin_url, instagram_handle,
		       facebook_url, empresa, cargo, website, localizacao,
		       status, pontuacao, canal_preferido, notas, tags,
		       criado_em, atualizado_em, ultimo_contato_em
		FROM leads WHERE email = $1 LIMIT 1`

	return r.scanLead(r.db.conn.QueryRowContext(ctx, query, strings.ToLower(email)))
}

// Listar retorna leads paginados com filtros opcionais.
func (r *LeadRepository) Listar(ctx context.Context, filtro ports.FiltroLead) ([]*domain.Lead, int, error) {
	if filtro.Limite <= 0 {
		filtro.Limite = 20
	}
	if filtro.Limite > 100 {
		filtro.Limite = 100
	}

	condicoes := []string{"1=1"}
	args := []any{}
	idx := 1

	if filtro.Status != nil {
		condicoes = append(condicoes, fmt.Sprintf("status = $%d", idx))
		args = append(args, string(*filtro.Status))
		idx++
	}
	if filtro.Canal != nil {
		condicoes = append(condicoes, fmt.Sprintf("canal_preferido = $%d", idx))
		args = append(args, string(*filtro.Canal))
		idx++
	}
	if filtro.PontuacaoMin > 0 {
		condicoes = append(condicoes, fmt.Sprintf("pontuacao >= $%d", idx))
		args = append(args, filtro.PontuacaoMin)
		idx++
	}
	if filtro.BuscaNome != "" {
		condicoes = append(condicoes, fmt.Sprintf("nome ILIKE $%d", idx))
		args = append(args, "%"+filtro.BuscaNome+"%")
		idx++
	}

	where := strings.Join(condicoes, " AND ")

	// Conta total para paginação
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM leads WHERE %s", where)
	if err := r.db.conn.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: falha ao contar leads: %w", err)
	}

	// Busca com paginação
	args = append(args, filtro.Limite, filtro.Offset)
	listQuery := fmt.Sprintf(`
		SELECT id, nome, email, telefone, linkedin_url, instagram_handle,
		       facebook_url, empresa, cargo, website, localizacao,
		       status, pontuacao, canal_preferido, notas, tags,
		       criado_em, atualizado_em, ultimo_contato_em
		FROM leads WHERE %s
		ORDER BY pontuacao DESC, criado_em DESC
		LIMIT $%d OFFSET $%d`, where, idx, idx+1)

	rows, err := r.db.conn.QueryContext(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: falha ao listar leads: %w", err)
	}
	defer rows.Close()

	var leads []*domain.Lead
	for rows.Next() {
		lead, err := r.scanLeadRow(rows)
		if err != nil {
			return nil, 0, err
		}
		leads = append(leads, lead)
	}

	return leads, total, rows.Err()
}

// Atualizar persiste as alterações de um lead existente.
func (r *LeadRepository) Atualizar(ctx context.Context, lead *domain.Lead) error {
	query := `
		UPDATE leads SET
			nome=$1, email=$2, telefone=$3, linkedin_url=$4, instagram_handle=$5,
			facebook_url=$6, empresa=$7, cargo=$8, website=$9, localizacao=$10,
			status=$11, pontuacao=$12, canal_preferido=$13, notas=$14, tags=$15,
			ultimo_contato_em=$16
		WHERE id=$17
		RETURNING atualizado_em`

	var canalPreferido *string
	if lead.CanalPreferido != "" {
		s := string(lead.CanalPreferido)
		canalPreferido = &s
	}

	return r.db.conn.QueryRowContext(ctx, query,
		lead.Nome, toNullStr(lead.Email), toNullStr(lead.Telefone),
		toNullStr(lead.LinkedInURL), toNullStr(lead.InstagramHandle),
		toNullStr(lead.FacebookURL), toNullStr(lead.Empresa),
		toNullStr(lead.Cargo), toNullStr(lead.Website), toNullStr(lead.Localizacao),
		string(lead.Status), lead.Pontuacao, canalPreferido,
		toNullStr(lead.Notas), pqArray(lead.Tags),
		lead.UltimoContatoEm, lead.ID,
	).Scan(&lead.AtualizadoEm)
}

// AtualizarStatus altera somente o campo status do lead.
func (r *LeadRepository) AtualizarStatus(ctx context.Context, id string, status domain.StatusLead) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE leads SET status=$1 WHERE id=$2`,
		string(status), id,
	)
	return err
}

// AtualizarPontuacao atualiza pontuação e status do lead de forma atômica.
func (r *LeadRepository) AtualizarPontuacao(ctx context.Context, id string, pontuacao int, status domain.StatusLead) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE leads SET pontuacao=$1, status=$2 WHERE id=$3`,
		pontuacao, string(status), id,
	)
	return err
}

// RegistrarInteracao insere um registro de engajamento do lead.
func (r *LeadRepository) RegistrarInteracao(ctx context.Context, leadID, tipo string, canal domain.Canal, dados map[string]any) error {
	dadosJSON, err := json.Marshal(dados)
	if err != nil {
		dadosJSON = []byte("{}")
	}
	_, err = r.db.conn.ExecContext(ctx,
		`INSERT INTO interacoes (lead_id, tipo, canal, dados) VALUES ($1,$2,$3,$4)`,
		leadID, tipo, string(canal), dadosJSON,
	)
	return err
}

// ContarInteracoes retorna quantas interações o lead teve.
func (r *LeadRepository) ContarInteracoes(ctx context.Context, leadID string) (int, error) {
	var total int
	err := r.db.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM interacoes WHERE lead_id=$1`,
		leadID,
	).Scan(&total)
	return total, err
}

// ============================================================
// JobRepository
// ============================================================

var _ ports.JobRepositoryPort = (*JobRepository)(nil)

// JobRepository gerencia a fila de trabalho.
type JobRepository struct{ db *DB }

// NovoJobRepository cria o repositório de jobs.
func NovoJobRepository(db *DB) *JobRepository { return &JobRepository{db: db} }

// Salvar insere um novo job na fila.
func (r *JobRepository) Salvar(ctx context.Context, job *domain.Job) error {
	query := `
		INSERT INTO jobs (lead_id, campanha_id, canal, prioridade, max_tentativas)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (lead_id, campanha_id, canal) DO NOTHING
		RETURNING id, criado_em`

	row := r.db.conn.QueryRowContext(ctx, query,
		job.Lead.ID, job.Campanha.ID, string(job.Canal),
		job.Prioridade, job.MaxTentativas,
	)
	return row.Scan(&job.ID, &job.CriadoEm)
}

// BuscarPendentes retorna jobs pendentes ordenados por prioridade.
// Usa SELECT FOR UPDATE SKIP LOCKED para evitar race conditions com múltiplos workers.
func (r *JobRepository) BuscarPendentes(ctx context.Context, limite int) ([]*domain.Job, error) {
	query := `
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
		LIMIT $1
		FOR UPDATE OF j SKIP LOCKED`

	rows, err := r.db.conn.QueryContext(ctx, query, limite)
	if err != nil {
		return nil, fmt.Errorf("postgres: falha ao buscar jobs pendentes: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.Job
	for rows.Next() {
		job := &domain.Job{
			Lead:     &domain.Lead{},
			Campanha: &domain.Campanha{},
		}
		var canal string
		err := rows.Scan(
			&job.ID, &job.Lead.ID, &job.Campanha.ID, &canal, &job.Prioridade,
			&job.Tentativas, &job.MaxTentativas,
			// lead fields
			&job.Lead.ID, &job.Lead.Nome, nullToStr(&job.Lead.Email),
			nullToStr(&job.Lead.Telefone), nullToStr(&job.Lead.LinkedInURL),
			nullToStr(&job.Lead.InstagramHandle), nullToStr(&job.Lead.FacebookURL),
			nullToStr(&job.Lead.Empresa), &job.Lead.Status, &job.Lead.Pontuacao,
			// campanha fields
			&job.Campanha.ID, &job.Campanha.Nome,
			nullToStr(&job.Campanha.TemplateAssunto), &job.Campanha.TemplateCorpo,
		)
		if err != nil {
			return nil, fmt.Errorf("postgres: falha ao ler job: %w", err)
		}
		job.Canal = domain.Canal(canal)
		jobs = append(jobs, job)
	}

	return jobs, rows.Err()
}

// MarcarProcessando atualiza o status do job para evitar duplo processamento.
func (r *JobRepository) MarcarProcessando(ctx context.Context, jobID string) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE jobs SET status='processando', tentativas=tentativas+1 WHERE id=$1`,
		jobID,
	)
	return err
}

// MarcarEnviado registra o sucesso do envio.
func (r *JobRepository) MarcarEnviado(ctx context.Context, jobID string, idExterno string) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE jobs SET status='enviado', processado_em=NOW(), dados_extras=dados_extras||$1::jsonb WHERE id=$2`,
		fmt.Sprintf(`{"id_externo":"%s"}`, idExterno), jobID,
	)
	return err
}

// MarcarFalhado registra a falha e decide se o job pode ser retentado.
func (r *JobRepository) MarcarFalhado(ctx context.Context, jobID string, erroStr string, podeRetentar bool) error {
	novoStatus := "cancelado"
	if podeRetentar {
		novoStatus = "pendente" // volta para a fila
	}
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE jobs SET status=$1, erro=$2, processado_em=NOW() WHERE id=$3`,
		novoStatus, toNullStr(erroStr), jobID,
	)
	return err
}

// CriarJobsParaCampanha cria jobs em lote para uma lista de leads.
func (r *JobRepository) CriarJobsParaCampanha(ctx context.Context, campanhaID string, leadsIDs []string, canal domain.Canal) error {
	if len(leadsIDs) == 0 {
		return nil
	}

	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: falha ao iniciar transação: %w", err)
	}
	defer tx.Rollback() //nolint

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO jobs (lead_id, campanha_id, canal, prioridade)
		VALUES ($1,$2,$3,5)
		ON CONFLICT (lead_id, campanha_id, canal) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("postgres: falha ao preparar statement: %w", err)
	}
	defer stmt.Close()

	for _, leadID := range leadsIDs {
		if _, err := stmt.ExecContext(ctx, leadID, campanhaID, string(canal)); err != nil {
			return fmt.Errorf("postgres: falha ao inserir job para lead '%s': %w", leadID, err)
		}
	}

	return tx.Commit()
}

// ============================================================
// CampanhaRepository
// ============================================================

var _ ports.CampanhaRepositoryPort = (*CampanhaRepository)(nil)

// CampanhaRepository gerencia campanhas no banco.
type CampanhaRepository struct{ db *DB }

// NovoCampanhaRepository cria o repositório de campanhas.
func NovoCampanhaRepository(db *DB) *CampanhaRepository { return &CampanhaRepository{db: db} }

// Salvar insere uma nova campanha.
func (r *CampanhaRepository) Salvar(ctx context.Context, campanha *domain.Campanha) error {
	canais := make([]string, len(campanha.Canais))
	for i, c := range campanha.Canais {
		canais[i] = string(c)
	}

	query := `
		INSERT INTO campanhas (nome, descricao, canais, template_assunto, template_corpo, max_leads)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, criado_em, atualizado_em`

	return r.db.conn.QueryRowContext(ctx, query,
		campanha.Nome, toNullStr(campanha.Descricao),
		pqArray(canais), toNullStr(campanha.TemplateAssunto),
		campanha.TemplateCorpo, campanha.MaxLeads,
	).Scan(&campanha.ID, &campanha.CriadoEm, &campanha.AtualizadoEm)
}

// BuscarPorID encontra uma campanha pelo UUID.
func (r *CampanhaRepository) BuscarPorID(ctx context.Context, id string) (*domain.Campanha, error) {
	query := `
		SELECT id, nome, descricao, status, canais, template_assunto,
		       template_corpo, max_leads, leads_processados,
		       criado_em, atualizado_em, iniciado_em, finalizado_em
		FROM campanhas WHERE id=$1`

	return r.scanCampanha(r.db.conn.QueryRowContext(ctx, query, id))
}

// Listar retorna campanhas paginadas.
func (r *CampanhaRepository) Listar(ctx context.Context, filtro ports.FiltroCampanha) ([]*domain.Campanha, int, error) {
	if filtro.Limite <= 0 {
		filtro.Limite = 20
	}

	where := "1=1"
	args := []any{}
	if filtro.Status != nil {
		where += " AND status=$1"
		args = append(args, string(*filtro.Status))
	}

	var total int
	r.db.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM campanhas WHERE "+where, args...).Scan(&total) //nolint

	args = append(args, filtro.Limite, filtro.Offset)
	idxL := len(args) - 1
	idxO := len(args)

	rows, err := r.db.conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, nome, descricao, status, canais, template_assunto,
		       template_corpo, max_leads, leads_processados,
		       criado_em, atualizado_em, iniciado_em, finalizado_em
		FROM campanhas WHERE %s
		ORDER BY criado_em DESC LIMIT $%d OFFSET $%d`, where, idxL, idxO), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var campanhas []*domain.Campanha
	for rows.Next() {
		c, err := r.scanCampanhaRow(rows)
		if err != nil {
			return nil, 0, err
		}
		campanhas = append(campanhas, c)
	}
	return campanhas, total, rows.Err()
}

// Atualizar persiste alterações em uma campanha existente.
func (r *CampanhaRepository) Atualizar(ctx context.Context, campanha *domain.Campanha) error {
	canais := make([]string, len(campanha.Canais))
	for i, c := range campanha.Canais {
		canais[i] = string(c)
	}
	_, err := r.db.conn.ExecContext(ctx, `
		UPDATE campanhas SET
			nome=$1, descricao=$2, canais=$3, template_assunto=$4,
			template_corpo=$5, max_leads=$6, status=$7,
			iniciado_em=$8, finalizado_em=$9
		WHERE id=$10`,
		campanha.Nome, toNullStr(campanha.Descricao),
		pqArray(canais), toNullStr(campanha.TemplateAssunto),
		campanha.TemplateCorpo, campanha.MaxLeads, string(campanha.Status),
		campanha.IniciadoEm, campanha.FinalizadoEm, campanha.ID,
	)
	return err
}

// AtualizarStatus muda apenas o status da campanha.
func (r *CampanhaRepository) AtualizarStatus(ctx context.Context, id string, status domain.StatusCampanha) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE campanhas SET status=$1 WHERE id=$2`,
		string(status), id,
	)
	return err
}

// ============================================================
// Helpers de scan
// ============================================================

func (r *LeadRepository) scanLead(row *sql.Row) (*domain.Lead, error) {
	lead := &domain.Lead{}
	var (
		email, telefone, linkedin, instagram, facebook    sql.NullString
		empresa, cargo, website, localizacao, notas, tags sql.NullString
		canalPreferido                                     sql.NullString
		ultimoContato                                      sql.NullTime
	)
	err := row.Scan(
		&lead.ID, &lead.Nome, &email, &telefone, &linkedin, &instagram,
		&facebook, &empresa, &cargo, &website, &localizacao,
		&lead.Status, &lead.Pontuacao, &canalPreferido, &notas, &tags,
		&lead.CriadoEm, &lead.AtualizadoEm, &ultimoContato,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("lead não encontrado")
	}
	if err != nil {
		return nil, err
	}

	lead.Email = email.String
	lead.Telefone = telefone.String
	lead.LinkedInURL = linkedin.String
	lead.InstagramHandle = instagram.String
	lead.FacebookURL = facebook.String
	lead.Empresa = empresa.String
	lead.Cargo = cargo.String
	lead.Website = website.String
	lead.Localizacao = localizacao.String
	lead.Notas = notas.String
	if tags.Valid {
		lead.Tags = parsePostgresTextArray(tags.String)
	}
	if canalPreferido.Valid {
		lead.CanalPreferido = domain.Canal(canalPreferido.String)
	}
	if ultimoContato.Valid {
		t := ultimoContato.Time
		lead.UltimoContatoEm = &t
	}
	return lead, nil
}

func (r *LeadRepository) scanLeadRow(rows *sql.Rows) (*domain.Lead, error) {
	lead := &domain.Lead{}
	var (
		email, telefone, linkedin, instagram, facebook    sql.NullString
		empresa, cargo, website, localizacao, notas, tags sql.NullString
		canalPreferido                                     sql.NullString
		ultimoContato                                      sql.NullTime
	)
	err := rows.Scan(
		&lead.ID, &lead.Nome, &email, &telefone, &linkedin, &instagram,
		&facebook, &empresa, &cargo, &website, &localizacao,
		&lead.Status, &lead.Pontuacao, &canalPreferido, &notas, &tags,
		&lead.CriadoEm, &lead.AtualizadoEm, &ultimoContato,
	)
	if err != nil {
		return nil, err
	}
	lead.Email = email.String
	lead.Telefone = telefone.String
	lead.LinkedInURL = linkedin.String
	lead.InstagramHandle = instagram.String
	lead.FacebookURL = facebook.String
	lead.Empresa = empresa.String
	lead.Cargo = cargo.String
	lead.Website = website.String
	lead.Localizacao = localizacao.String
	lead.Notas = notas.String
	if tags.Valid {
		lead.Tags = parsePostgresTextArray(tags.String)
	}
	if canalPreferido.Valid {
		lead.CanalPreferido = domain.Canal(canalPreferido.String)
	}
	if ultimoContato.Valid {
		t := ultimoContato.Time
		lead.UltimoContatoEm = &t
	}
	return lead, nil
}

func (r *CampanhaRepository) scanCampanha(row *sql.Row) (*domain.Campanha, error) {
	c := &domain.Campanha{}
	var (
		descricao, assunto sql.NullString
		canaisStr          sql.NullString
		iniciadoEm         sql.NullTime
		finalizadoEm       sql.NullTime
	)
	err := row.Scan(
		&c.ID, &c.Nome, &descricao, &c.Status, &canaisStr, &assunto,
		&c.TemplateCorpo, &c.MaxLeads, &c.LeadsProcessados,
		&c.CriadoEm, &c.AtualizadoEm, &iniciadoEm, &finalizadoEm,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("campanha não encontrada")
	}
	if err != nil {
		return nil, err
	}
	c.Descricao = descricao.String
	c.TemplateAssunto = assunto.String
	if canaisStr.Valid {
		c.Canais = parsePostgresArray(canaisStr.String)
	}
	if iniciadoEm.Valid {
		t := iniciadoEm.Time
		c.IniciadoEm = &t
	}
	if finalizadoEm.Valid {
		t := finalizadoEm.Time
		c.FinalizadoEm = &t
	}
	return c, nil
}

func (r *CampanhaRepository) scanCampanhaRow(rows *sql.Rows) (*domain.Campanha, error) {
	c := &domain.Campanha{}
	var (
		descricao, assunto sql.NullString
		canaisStr          sql.NullString
		iniciadoEm         sql.NullTime
		finalizadoEm       sql.NullTime
	)
	err := rows.Scan(
		&c.ID, &c.Nome, &descricao, &c.Status, &canaisStr, &assunto,
		&c.TemplateCorpo, &c.MaxLeads, &c.LeadsProcessados,
		&c.CriadoEm, &c.AtualizadoEm, &iniciadoEm, &finalizadoEm,
	)
	if err != nil {
		return nil, err
	}
	c.Descricao = descricao.String
	c.TemplateAssunto = assunto.String
	if canaisStr.Valid {
		c.Canais = parsePostgresArray(canaisStr.String)
	}
	if iniciadoEm.Valid {
		t := iniciadoEm.Time
		c.IniciadoEm = &t
	}
	if finalizadoEm.Valid {
		t := finalizadoEm.Time
		c.FinalizadoEm = &t
	}
	return c, nil
}

// ============================================================
// Helpers de conversão de tipos
// ============================================================

func toNullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// strScanner implementa sql.Scanner escrevendo o valor lido em *dest.
// Substitui o padrão sql.NullString para campos opcionais no Scan.
type strScanner struct{ dest *string }

func (s *strScanner) Scan(src any) error {
	if src == nil {
		*s.dest = ""
		return nil
	}
	switch v := src.(type) {
	case string:
		*s.dest = v
	case []byte:
		*s.dest = string(v)
	default:
		*s.dest = fmt.Sprintf("%v", v)
	}
	return nil
}

// nullToStr retorna um sql.Scanner que popula *dest com o valor lido do banco.
func nullToStr(dest *string) sql.Scanner {
	return &strScanner{dest: dest}
}

// parsePostgresArray converte o formato de array do PostgreSQL ({val1,val2}) para []domain.Canal.
func parsePostgresArray(s string) []domain.Canal {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	canais := make([]domain.Canal, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p != "" {
			canais = append(canais, domain.Canal(p))
		}
	}
	return canais
}

// parsePostgresTextArray converte o formato de array do PostgreSQL ({val1,val2}) para []string.
func parsePostgresTextArray(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// pqArray converte []string para o formato de array do PostgreSQL com escape correto.
func pqArray(ss []string) string {
	if len(ss) == 0 {
		return "{}"
	}
	parts := make([]string, len(ss))
	for i, s := range ss {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts[i] = `"` + s + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}
