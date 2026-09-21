package httpapi

import (
	"io"
	"net/http"
	"riftledger/internal/app"
	"riftledger/internal/sqlite"
	"strings"
)

func (a *API) messageRoutes(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	switch {
	case path == "/api/message-templates" && r.Method == "GET":
		v, e := a.Service.MessageTemplates()
		a.respond(w, v, e)
	case path == "/api/message-templates" && r.Method == "POST":
		var p app.TemplatePatch
		if e := decode(w, r, &p); e != nil {
			a.respond(w, nil, e)
			return true
		}
		a.command(w, r, p, func(tx *sqlite.Tx) (any, error) { return a.Service.SaveMessageTemplate(tx, p) })
	case path == "/api/message-templates/preview" && r.Method == "POST":
		var p app.TemplatePatch
		if e := decode(w, r, &p); e != nil {
			a.respond(w, nil, e)
			return true
		}
		v, e := a.Service.PreviewMessageTemplate(p)
		a.respond(w, v, e)
	case path == "/api/message-images" && r.Method == "POST":
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, app.MaxMessageImage))
		if e != nil {
			a.respond(w, nil, &app.Fault{Code: 400, Message: "图片不能超过2MB"})
			return true
		}
		v, e := a.Service.SaveMessageImage(raw)
		a.respond(w, v, e)
	case strings.HasPrefix(path, "/api/message-images/") && r.Method == "GET":
		raw, mime, e := a.Service.MessageImage(strings.TrimPrefix(path, "/api/message-images/"))
		if e != nil {
			a.respond(w, nil, e)
			return true
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(raw)
	default:
		return false
	}
	return true
}
