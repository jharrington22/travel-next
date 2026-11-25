package server

import (
	"embed"
	"html/template"
	"io/fs"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	v1 "github.com/jamesh/travel-next/internal/api/v1"
	"github.com/jamesh/travel-next/internal/auth"
)

func newSubFS(f embed.FS, dir string) (fs.FS, error) { return fs.Sub(f, dir) }

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"prettyDate": func(iso string) string {
			t, err := time.Parse("2006-01-02", iso)
			if err != nil {
				return iso
			}
			return t.Format("Mon 2 Jan 2006")
		},
		"shortDate": func(iso string) string {
			t, err := time.Parse("2006-01-02", iso)
			if err != nil {
				return iso
			}
			return t.Format("2 Jan")
		},
		"weekday": func(iso string) string {
			t, err := time.Parse("2006-01-02", iso)
			if err != nil {
				return ""
			}
			return strings.ToUpper(t.Format("Mon"))
		},
		"monthDay": func(iso string) string {
			t, err := time.Parse("2006-01-02", iso)
			if err != nil {
				return iso
			}
			return strings.ToUpper(t.Format("Jan 2"))
		},
	}
}

// page builds the common template payload.
func (s *Server) page(c *gin.Context, extra gin.H) gin.H {
	data := gin.H{
		"User":    auth.CurrentUser(c),
		"CSRF":    auth.CSRFToken(c),
		"BaseURL": s.cfg.BaseURL,
		"Path":    c.Request.URL.Path,
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (s *Server) render(c *gin.Context, status int, name string, data gin.H) {
	c.HTML(status, name, data)
}

func (s *Server) renderError(c *gin.Context, status int, title, detail string) {
	c.HTML(status, "error.html", s.page(c, gin.H{"Title": title, "Detail": detail, "Status": status}))
}

func errorBody(code, message string) v1.Error {
	return v1.Error{Error: v1.ErrorBody{Code: code, Message: message}}
}
