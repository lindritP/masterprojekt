// Package pgrepo is the portable CaseRepository adapter (SRS §7.3): plain
// PostgreSQL through pgx with password authentication. It uses no extension
// and no provider-specific SQL (FR-033).
//
// Every write goes through one transaction that locks the case row
// (READ COMMITTED + SELECT ... FOR UPDATE, FR-031), applies the domain
// operation, and writes the case, its documents, new validation results and
// the audit entries together (INV-3).
package pgrepo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Config mirrors the CCSRA_DB_* variables (SRS §9.3).
type Config struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string // CCSRA_DB_SSLMODE, default "require"
	MaxConns int
}

// Repo implements ports.CaseRepository.
type Repo struct {
	pool *pgxpool.Pool
}

var _ ports.CaseRepository = (*Repo)(nil)

// validationConsumer names the idempotency scope of validation results in
// processed_messages.
const validationConsumer = "validation-result"

func New(ctx context.Context, cfg Config) (*Repo, error) {
	if cfg.SSLMode == "" {
		cfg.SSLMode = "require"
	}
	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:     cfg.Name,
		RawQuery: url.Values{"sslmode": {cfg.SSLMode}}.Encode(),
	}
	pc, err := pgxpool.ParseConfig(dsn.String())
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns > 0 {
		pc.MaxConns = int32(cfg.MaxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	return &Repo{pool: pool}, nil
}

func (r *Repo) Close() { r.pool.Close() }

// Ping checks connectivity, for /readyz.
func (r *Repo) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

func (r *Repo) AllocateReference(ctx context.Context, year int) (string, error) {
	var seq int64
	if err := r.pool.QueryRow(ctx, `SELECT nextval('case_reference_seq')`).Scan(&seq); err != nil {
		return "", fmt.Errorf("allocate reference: %w", err)
	}
	return domain.FormatReference(year, seq), nil
}

func (r *Repo) Create(ctx context.Context, c *domain.Case, audit []domain.AuditEntry) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO cases (id, reference, case_type, state, subject_id, title, version,
			                   created_at, updated_at, submitted_at, decided_at, decided_by, decision_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			c.ID, c.Reference, string(c.Type), string(c.State), c.SubjectID, c.Title, c.Version,
			c.CreatedAt, c.UpdatedAt, c.SubmittedAt, c.DecidedAt, nullString(c.DecidedBy), nullString(c.DecisionReason))
		if err != nil {
			return fmt.Errorf("insert case: %w", err)
		}
		if err := writeDocuments(ctx, tx, c); err != nil {
			return err
		}
		return writeAudit(ctx, tx, audit)
	})
}

func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*domain.Case, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	return loadCase(ctx, tx, id, false)
}

func (r *Repo) Transition(ctx context.Context, id uuid.UUID, expectedVersion int, fn func(*domain.Case) ([]domain.AuditEntry, error)) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		c, err := loadCase(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if expectedVersion != 0 {
			if err := c.ExpectVersion(expectedVersion); err != nil {
				return err
			}
		}
		return apply(ctx, tx, c, fn)
	})
}

func (r *Repo) TransitionOnce(ctx context.Context, scope, key string, id uuid.UUID, expectedVersion int, fn func(*domain.Case) ([]domain.AuditEntry, error)) (bool, error) {
	replayed := false
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		c, err := loadCase(ctx, tx, id, true)
		if err != nil {
			return err
		}
		// The case row lock serialises concurrent requests with the same key.
		tag, err := tx.Exec(ctx, `
			INSERT INTO processed_messages (message_id, consumer, result_ref) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, key, "http:"+scope, id.String())
		if err != nil {
			return fmt.Errorf("record idempotency key: %w", err)
		}
		if tag.RowsAffected() == 0 {
			replayed = true
			return nil
		}
		if expectedVersion != 0 {
			if err := c.ExpectVersion(expectedVersion); err != nil {
				return err
			}
		}
		return apply(ctx, tx, c, fn)
	})
	return replayed, err
}

func (r *Repo) CaseIDOfDocument(ctx context.Context, documentID uuid.UUID) (uuid.UUID, error) {
	var caseID uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT case_id FROM documents WHERE id = $1`, documentID).Scan(&caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("document %s: %w", documentID, ports.ErrNotFound)
	}
	return caseID, err
}

func (r *Repo) RecordValidationResult(ctx context.Context, idempotencyKey string, res domain.ValidationResult, op domain.Op) (bool, error) {
	replayed := false
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO processed_messages (message_id, consumer, result_ref) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, idempotencyKey, validationConsumer, res.DocumentID.String())
		if err != nil {
			return fmt.Errorf("record idempotency key: %w", err)
		}
		if tag.RowsAffected() == 0 {
			replayed = true
			return nil
		}

		var caseID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT case_id FROM documents WHERE id = $1`, res.DocumentID).Scan(&caseID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &domain.Error{Code: domain.CodeDocumentNotFound, Detail: fmt.Sprintf("document %s not found", res.DocumentID)}
		}
		if err != nil {
			return err
		}
		c, err := loadCase(ctx, tx, caseID, true)
		if err != nil {
			return err
		}
		return apply(ctx, tx, c, func(c *domain.Case) ([]domain.AuditEntry, error) {
			audit, rep, err := c.RecordValidation(res, op)
			replayed = rep
			return audit, err
		})
	})
	return replayed, err
}

func (r *Repo) List(ctx context.Context, f ports.ListFilter) ([]domain.CaseSummary, ports.Cursor, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if f.SubjectID != "" {
		where = append(where, "c.subject_id = "+arg(f.SubjectID))
	}
	if f.State != "" {
		where = append(where, "c.state = "+arg(string(f.State)))
	}
	if f.CaseType != "" {
		where = append(where, "c.case_type = "+arg(string(f.CaseType)))
	}
	if f.After != "" {
		at, id, err := decodeCursor(f.After)
		if err != nil {
			return nil, "", err
		}
		where = append(where, fmt.Sprintf("(c.created_at, c.id) < (%s, %s)", arg(at), arg(id)))
	}
	q := `SELECT c.id, c.reference, c.case_type, c.state, c.title, c.created_at, c.updated_at,
	             (SELECT count(*) FROM documents d WHERE d.case_id = c.id AND d.status NOT IN ('SUPERSEDED','DELETED'))
	      FROM cases c`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY c.created_at DESC, c.id DESC LIMIT " + arg(limit+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list cases: %w", err)
	}
	defer rows.Close()
	var out []domain.CaseSummary
	for rows.Next() {
		var s domain.CaseSummary
		var ct, st string
		if err := rows.Scan(&s.ID, &s.Reference, &ct, &st, &s.Title, &s.CreatedAt, &s.UpdatedAt, &s.DocumentCount); err != nil {
			return nil, "", err
		}
		s.Type, s.State = domain.CaseType(ct), domain.State(st)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var next ports.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, next, nil
}

func (r *Repo) ListAudit(ctx context.Context, caseID uuid.UUID, limit int, after ports.Cursor) ([]domain.AuditEntry, ports.Cursor, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var afterID int64
	if after != "" {
		var err error
		if afterID, err = strconv.ParseInt(string(after), 10, 64); err != nil {
			return nil, "", fmt.Errorf("audit cursor %q: %w", after, ports.ErrBadCursor)
		}
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, case_id, occurred_at, actor_type, actor_id, event_type, correlation_id, payload
		FROM audit_events WHERE case_id = $1 AND id > $2 ORDER BY id LIMIT $3`, caseID, afterID, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	var out []domain.AuditEntry
	for rows.Next() {
		var a domain.AuditEntry
		var actorType string
		var payload []byte
		if err := rows.Scan(&a.ID, &a.CaseID, &a.OccurredAt, &actorType, &a.ActorID, &a.EventType, &a.CorrelationID, &payload); err != nil {
			return nil, "", err
		}
		a.ActorType = domain.ActorType(actorType)
		if err := json.Unmarshal(payload, &a.Payload); err != nil {
			return nil, "", err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var next ports.Cursor
	if len(out) > limit {
		out = out[:limit]
		next = ports.Cursor(strconv.FormatInt(out[limit-1].ID, 10))
	}
	return out, next, nil
}

// apply runs fn on c and writes the result. An operation that changes
// nothing (an idempotent repeat) writes nothing.
func apply(ctx context.Context, tx pgx.Tx, c *domain.Case, fn func(*domain.Case) ([]domain.AuditEntry, error)) error {
	before := c.Version
	audit, err := fn(c)
	if err != nil {
		return err
	}
	if c.Version == before {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE cases SET state = $3, title = $4, version = $5, updated_at = $6, submitted_at = $7,
		                 decided_at = $8, decided_by = $9, decision_reason = $10
		WHERE id = $1 AND version = $2`,
		c.ID, before, string(c.State), c.Title, c.Version, c.UpdatedAt, c.SubmittedAt,
		c.DecidedAt, nullString(c.DecidedBy), nullString(c.DecisionReason))
	if err != nil {
		return fmt.Errorf("update case: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return &domain.Error{Code: domain.CodeConcurrentModification, Detail: "case changed concurrently"}
	}
	if err := writeDocuments(ctx, tx, c); err != nil {
		return err
	}
	return writeAudit(ctx, tx, audit)
}

// writeDocuments upserts every document in aggregate order, so a superseded
// document is updated before its replacement is inserted (INV-7 index).
// storage_key is never updated (INV-4).
func writeDocuments(ctx context.Context, tx pgx.Tx, c *domain.Case) error {
	for _, d := range c.Documents {
		_, err := tx.Exec(ctx, `
			INSERT INTO documents (id, case_id, filename, declared_mime, detected_mime, size_bytes, sha256,
			                       storage_key, status, supersedes_id, created_at, uploaded_at, validated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO UPDATE SET
				detected_mime = EXCLUDED.detected_mime, size_bytes = EXCLUDED.size_bytes,
				sha256 = EXCLUDED.sha256, status = EXCLUDED.status, supersedes_id = EXCLUDED.supersedes_id,
				uploaded_at = EXCLUDED.uploaded_at, validated_at = EXCLUDED.validated_at`,
			d.ID, d.CaseID, d.Filename, d.DeclaredMime, nullString(d.DetectedMime), nullInt(d.SizeBytes),
			nullString(d.SHA256), d.StorageKey, string(d.Status), d.SupersedesID, d.CreatedAt, d.UploadedAt, d.ValidatedAt)
		if err != nil {
			return fmt.Errorf("write document %s: %w", d.ID, err)
		}
		if d.Result != nil && d.Result.ID == uuid.Nil {
			if err := insertResult(ctx, tx, d); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertResult(ctx context.Context, tx pgx.Tx, d *domain.Document) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	checks, err := json.Marshal(d.Result.Checks)
	if err != nil {
		return err
	}
	codes := d.Result.FailureCodes
	if codes == nil {
		codes = []string{}
	}
	createdAt := time.Now().UTC()
	if d.ValidatedAt != nil {
		createdAt = *d.ValidatedAt
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO validation_results (id, document_id, outcome, failure_codes, checks, worker_version, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, d.ID, string(d.Result.Outcome), codes, checks, d.Result.WorkerVersion, d.Result.DurationMs, createdAt)
	if err != nil {
		return fmt.Errorf("insert validation result for %s: %w", d.ID, err)
	}
	d.Result.ID, d.Result.CreatedAt = id, createdAt
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, audit []domain.AuditEntry) error {
	for i := range audit {
		a := &audit[i]
		payload, err := json.Marshal(a.Payload)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO audit_events (case_id, occurred_at, actor_type, actor_id, event_type, correlation_id, payload)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			a.CaseID, a.OccurredAt, string(a.ActorType), a.ActorID, a.EventType, a.CorrelationID, payload).Scan(&a.ID)
		if err != nil {
			return fmt.Errorf("insert audit entry: %w", err)
		}
	}
	return nil
}

// loadCase reads a case with its documents and their validation results,
// optionally locking the case row.
func loadCase(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (*domain.Case, error) {
	q := `SELECT id, reference, case_type, state, subject_id, title, version, created_at, updated_at,
	             submitted_at, decided_at, coalesce(decided_by, ''), coalesce(decision_reason, '')
	      FROM cases WHERE id = $1`
	if lock {
		q += " FOR UPDATE"
	}
	c := &domain.Case{}
	var ct, st string
	err := tx.QueryRow(ctx, q, id).Scan(&c.ID, &c.Reference, &ct, &st, &c.SubjectID, &c.Title, &c.Version,
		&c.CreatedAt, &c.UpdatedAt, &c.SubmittedAt, &c.DecidedAt, &c.DecidedBy, &c.DecisionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("case %s: %w", id, ports.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load case: %w", err)
	}
	c.Type, c.State = domain.CaseType(ct), domain.State(st)

	rows, err := tx.Query(ctx, `
		SELECT d.id, d.case_id, d.filename, d.declared_mime, coalesce(d.detected_mime, ''), coalesce(d.size_bytes, 0),
		       coalesce(d.sha256, ''), d.storage_key, d.status, d.supersedes_id, d.created_at, d.uploaded_at, d.validated_at,
		       v.id, v.outcome, v.failure_codes, v.checks, v.worker_version, v.duration_ms, v.created_at
		FROM documents d LEFT JOIN validation_results v ON v.document_id = d.id
		WHERE d.case_id = $1 ORDER BY d.created_at, d.id`, id)
	if err != nil {
		return nil, fmt.Errorf("load documents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		d := &domain.Document{}
		var status string
		var supersedes, resultID uuid.NullUUID
		var outcome, workerVersion *string
		var codes []string
		var checks []byte
		var duration *int
		var resultAt *time.Time
		if err := rows.Scan(&d.ID, &d.CaseID, &d.Filename, &d.DeclaredMime, &d.DetectedMime, &d.SizeBytes,
			&d.SHA256, &d.StorageKey, &status, &supersedes, &d.CreatedAt, &d.UploadedAt, &d.ValidatedAt,
			&resultID, &outcome, &codes, &checks, &workerVersion, &duration, &resultAt); err != nil {
			return nil, err
		}
		d.Status = domain.DocumentStatus(status)
		if supersedes.Valid {
			d.SupersedesID = &supersedes.UUID
		}
		if resultID.Valid {
			res := &domain.ValidationResult{
				ID: resultID.UUID, DocumentID: d.ID, Outcome: domain.Outcome(*outcome),
				WorkerVersion: *workerVersion, DurationMs: *duration, CreatedAt: *resultAt,
			}
			if len(codes) > 0 {
				res.FailureCodes = codes
			}
			if err := json.Unmarshal(checks, &res.Checks); err != nil {
				return nil, err
			}
			d.Result = res
		}
		c.Documents = append(c.Documents, d)
	}
	return c, rows.Err()
}

func encodeCursor(at time.Time, id uuid.UUID) ports.Cursor {
	return ports.Cursor(base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String())))
}

func decodeCursor(c ports.Cursor) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil {
		return time.Time{}, uuid.Nil, ports.ErrBadCursor
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ports.ErrBadCursor
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, ports.ErrBadCursor
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, ports.ErrBadCursor
	}
	return at, id, nil
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullInt(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}
