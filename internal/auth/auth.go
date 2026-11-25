// Package auth handles passwords, cookie sessions, API tokens and the Gin
// middleware that puts the current user on the request context.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/store"
)

const (
	SessionCookie = "tn_session"
	ctxUserKey    = "currentUser"
	ctxSessionKey = "currentSession"
	tokenPrefix   = "tnx_"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	usernameRe            = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,30}$`)
	emailRe               = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

type Service struct {
	store      *store.Store
	sessionTTL time.Duration
	secure     bool
}

func NewService(s *store.Store, sessionTTL time.Duration, cookieSecure bool) *Service {
	return &Service{store: s, sessionTTL: sessionTTL, secure: cookieSecure}
}

// ---------- validation ----------

func NormaliseUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return errors.New("username must be 3-31 characters: lowercase letters, numbers, dash or underscore, starting with a letter or number")
	}
	switch u {
	case "api", "login", "logout", "signup", "new", "static", "health", "healthz", "readyz",
		"dashboard", "docs", "u", "admin", "settings", "me", "about":
		return errors.New("that username is reserved")
	}
	return nil
}

func ValidateEmail(e string) error {
	if !emailRe.MatchString(e) || len(e) > 254 {
		return errors.New("enter a valid email address")
	}
	return nil
}

// ValidatePassword enforces a length floor and a little variety, which is a
// better trade than a long list of rules users work around.
func ValidatePassword(p string) error {
	if len(p) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(p) > 128 {
		return errors.New("password must be at most 128 characters")
	}
	var hasLetter, hasOther bool
	for _, r := range p {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		default:
			hasOther = true
		}
	}
	if !hasLetter || !hasOther {
		return errors.New("password must mix letters with at least one number or symbol")
	}
	return nil
}

// ---------- registration and login ----------

func (s *Service) Register(ctx context.Context, username, email, displayName, password string) (*models.User, error) {
	username = NormaliseUsername(username)
	email = strings.ToLower(strings.TrimSpace(email))
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = username
	}
	u := &models.User{
		Username:     username,
		Email:        email,
		DisplayName:  strings.TrimSpace(displayName),
		PasswordHash: string(hash),
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, errors.New("that username or email is already registered")
		}
		return nil, err
	}
	return u, nil
}

func (s *Service) Login(ctx context.Context, login, password string) (*models.User, error) {
	u, err := s.store.UserByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Spend comparable time on an unknown user so the response does not
			// leak which accounts exist.
			_ = bcrypt.CompareHashAndPassword(
				[]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// ---------- sessions ----------

func (s *Service) StartSession(c *gin.Context, u *models.User) (*models.Session, error) {
	sess := &models.Session{
		ID:        randomToken(32),
		UserID:    u.ID,
		CSRFToken: randomToken(24),
		ExpiresAt: time.Now().Add(s.sessionTTL),
	}
	if err := s.store.CreateSession(c.Request.Context(), sess, c.Request.UserAgent()); err != nil {
		return nil, err
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookie, sess.ID, int(s.sessionTTL.Seconds()), "/", "", s.secure, true)
	return sess, nil
}

func (s *Service) EndSession(c *gin.Context) {
	if id, err := c.Cookie(SessionCookie); err == nil && id != "" {
		_ = s.store.DeleteSession(c.Request.Context(), id)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookie, "", -1, "/", "", s.secure, true)
}

// ---------- API tokens ----------

// IssueAPIToken returns the token record with Secret set. The secret is shown
// once and only its SHA-256 is stored.
func (s *Service) IssueAPIToken(ctx context.Context, u *models.User, name string) (*models.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "api token"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	secret := tokenPrefix + randomToken(32)
	t := &models.APIToken{UserID: u.ID, Name: name, Prefix: secret[:len(tokenPrefix)+8]}
	if err := s.store.CreateAPIToken(ctx, t, hashToken(secret)); err != nil {
		return nil, err
	}
	t.Secret = secret
	return t, nil
}

func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// ---------- middleware ----------

// Resolve attaches the current user to the context when the request carries a
// valid session cookie or bearer token. It never rejects a request itself.
func (s *Service) Resolve() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			secret := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			if secret != "" {
				if u, err := s.store.UserByTokenHash(ctx, hashToken(secret)); err == nil {
					c.Set(ctxUserKey, u)
					c.Next()
					return
				}
			}
		}
		if id, err := c.Cookie(SessionCookie); err == nil && id != "" {
			if sess, u, err := s.store.SessionWithUser(ctx, id); err == nil {
				c.Set(ctxUserKey, u)
				c.Set(ctxSessionKey, sess)
			}
		}
		c.Next()
	}
}

// RequireWeb redirects anonymous browsers to the login page.
func RequireWeb() gin.HandlerFunc {
	return func(c *gin.Context) {
		if CurrentUser(c) == nil {
			next := c.Request.URL.RequestURI()
			c.Redirect(http.StatusSeeOther, "/login?next="+urlEscape(next))
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireAPI rejects anonymous API calls with RFC-shaped JSON.
func RequireAPI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if CurrentUser(c) == nil {
			c.Header("WWW-Authenticate", `Bearer realm="travel-next"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "unauthorized",
					"message": "Authenticate with a Bearer API token or a browser session.",
				},
			})
			return
		}
		c.Next()
	}
}

// VerifyCSRF guards cookie-authenticated form posts. Bearer-token callers are
// exempt: a token is not sent automatically by a browser, so there is nothing
// to forge.
func VerifyCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		sess := CurrentSession(c)
		if sess == nil {
			c.Next()
			return
		}
		sent := c.PostForm("csrf_token")
		if sent == "" {
			sent = c.GetHeader("X-CSRF-Token")
		}
		if subtle.ConstantTimeCompare([]byte(sent), []byte(sess.CSRFToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "csrf_failed", "message": "Form token missing or stale. Reload the page and try again."},
			})
			return
		}
		c.Next()
	}
}

func CurrentUser(c *gin.Context) *models.User {
	if v, ok := c.Get(ctxUserKey); ok {
		if u, ok := v.(*models.User); ok {
			return u
		}
	}
	return nil
}

func CurrentSession(c *gin.Context) *models.Session {
	if v, ok := c.Get(ctxSessionKey); ok {
		if s, ok := v.(*models.Session); ok {
			return s
		}
	}
	return nil
}

func CSRFToken(c *gin.Context) string {
	if s := CurrentSession(c); s != nil {
		return s.CSRFToken
	}
	return ""
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is unrecoverable; refusing to issue a weak token
		// is the only safe response.
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func urlEscape(s string) string {
	r := strings.NewReplacer("&", "%26", "?", "%3F", "#", "%23", " ", "%20", "\"", "%22", "<", "%3C", ">", "%3E")
	return r.Replace(s)
}
