// Package store is the Postgres persistence layer.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jamesh/travel-next/internal/models"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// WaitForDB retries Ping until the deadline, so the container can start before
// Postgres is accepting connections.
func (s *Store) WaitForDB(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if last = s.Ping(ctx); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("database not ready: %w", last)
}

// Migrate applies every embedded migration that has not run yet.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ---------- users ----------

func (s *Store) CreateUser(ctx context.Context, u *models.User) error {
	u.ID = uuid.New()
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, username, email, display_name, password_hash)
		 VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		u.ID, u.Username, u.Email, u.DisplayName, u.PasswordHash).Scan(&u.CreatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

const userCols = `id, username, email, display_name, password_hash, created_at`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// UserByLogin accepts either a username or an email address.
func (s *Store) UserByLogin(ctx context.Context, login string) (*models.User, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE username = $1 OR email = $1`, login))
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*models.User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE username = $1`, strings.ToLower(username)))
}

// ---------- sessions ----------

func (s *Store) CreateSession(ctx context.Context, sess *models.Session, userAgent string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, csrf_token, user_agent, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		sess.ID, sess.UserID, sess.CSRFToken, userAgent, sess.ExpiresAt)
	return err
}

func (s *Store) SessionWithUser(ctx context.Context, id string) (*models.Session, *models.User, error) {
	var sess models.Session
	var u models.User
	err := s.pool.QueryRow(ctx,
		`SELECT s.id, s.user_id, s.csrf_token, s.expires_at,
		        u.id, u.username, u.email, u.display_name, u.password_hash, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1 AND s.expires_at > now()`, id).
		Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.ExpiresAt,
			&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return &sess, &u, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------- api tokens ----------

func (s *Store) CreateAPIToken(ctx context.Context, t *models.APIToken, hash string) error {
	t.ID = uuid.New()
	return s.pool.QueryRow(ctx,
		`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash)
		 VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
		t.ID, t.UserID, t.Name, t.Prefix, hash).Scan(&t.CreatedAt)
}

func (s *Store) UserByTokenHash(ctx context.Context, hash string) (*models.User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT u.id, u.username, u.email, u.display_name, u.password_hash, u.created_at
		 FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = $1`, hash))
	if err != nil {
		return nil, err
	}
	// Best-effort touch; a failure here must not fail the request.
	_, _ = s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE token_hash = $1`, hash)
	return u, nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID uuid.UUID) ([]models.APIToken, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, prefix, created_at, last_used_at FROM api_tokens
		 WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.APIToken
	for rows.Next() {
		var t models.APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAPIToken(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- itineraries ----------

const itinCols = `i.id, i.user_id, u.username, i.slug, i.title, i.status, i.is_public,
	i.model, i.error, i.request, i.content, i.created_at, i.updated_at`

func scanItinerary(row pgx.Row) (*models.Itinerary, error) {
	var (
		it      models.Itinerary
		reqRaw  []byte
		contRaw []byte
	)
	err := row.Scan(&it.ID, &it.UserID, &it.Username, &it.Slug, &it.Title, &it.Status, &it.Public,
		&it.Model, &it.Error, &reqRaw, &contRaw, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(reqRaw) > 0 {
		if err := json.Unmarshal(reqRaw, &it.Request); err != nil {
			return nil, fmt.Errorf("decode request: %w", err)
		}
	}
	if len(contRaw) > 0 {
		doc := &models.ItineraryDoc{}
		if err := json.Unmarshal(contRaw, doc); err != nil {
			return nil, fmt.Errorf("decode content: %w", err)
		}
		it.Content = doc
	}
	return &it, nil
}

// SlugTaken reports whether the user already has an itinerary at this slug.
func (s *Store) SlugTaken(ctx context.Context, userID uuid.UUID, slug string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM itineraries WHERE user_id = $1 AND slug = $2)`, userID, slug).Scan(&exists)
	return exists, err
}

func (s *Store) CreateItinerary(ctx context.Context, it *models.Itinerary) error {
	it.ID = uuid.New()
	req, err := json.Marshal(it.Request)
	if err != nil {
		return err
	}
	err = s.pool.QueryRow(ctx,
		`INSERT INTO itineraries (id, user_id, slug, title, status, is_public, request)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at, updated_at`,
		it.ID, it.UserID, it.Slug, it.Title, it.Status, it.Public, req).Scan(&it.CreatedAt, &it.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func (s *Store) SaveItineraryContent(ctx context.Context, id uuid.UUID, doc *models.ItineraryDoc, model string) error {
	content, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	title := doc.Title
	if title == "" {
		title = doc.Destination
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE itineraries SET content = $2, title = $3, model = $4, status = $5, error = '', updated_at = now()
		 WHERE id = $1`, id, content, title, model, models.StatusReady)
	return err
}

func (s *Store) FailItinerary(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE itineraries SET status = $2, error = $3, updated_at = now() WHERE id = $1`,
		id, models.StatusFailed, reason)
	return err
}

func (s *Store) SetItineraryVisibility(ctx context.Context, userID, id uuid.UUID, public bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE itineraries SET is_public = $3, updated_at = now() WHERE id = $1 AND user_id = $2`,
		id, userID, public)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteItinerary(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM itineraries WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ItineraryByID(ctx context.Context, id uuid.UUID) (*models.Itinerary, error) {
	return scanItinerary(s.pool.QueryRow(ctx,
		`SELECT `+itinCols+` FROM itineraries i JOIN users u ON u.id = i.user_id WHERE i.id = $1`, id))
}

func (s *Store) ItineraryBySlug(ctx context.Context, username, slug string) (*models.Itinerary, error) {
	return scanItinerary(s.pool.QueryRow(ctx,
		`SELECT `+itinCols+` FROM itineraries i JOIN users u ON u.id = i.user_id
		 WHERE u.username = $1 AND i.slug = $2`, strings.ToLower(username), slug))
}

func (s *Store) ListItineraries(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.Itinerary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+itinCols+` FROM itineraries i JOIN users u ON u.id = i.user_id
		 WHERE i.user_id = $1 ORDER BY i.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Itinerary
	for rows.Next() {
		it, err := scanItinerary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// GenerationsToday counts itineraries created in the last 24h, for rate limiting.
func (s *Store) GenerationsToday(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM itineraries WHERE user_id = $1 AND created_at > now() - interval '24 hours'`,
		userID).Scan(&n)
	return n, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
