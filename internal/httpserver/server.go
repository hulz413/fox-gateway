package httpserver

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type ReadyReporter func() (int, any)

type Server struct {
	httpServer *http.Server
	listener   net.Listener
}

func New(addr string, lark *LarkHandler, requestLogger *log.Logger, ready ...ReadyReporter) *Server {
	r := newRouter(lark, requestLogger, firstReadyReporter(ready))
	return &Server{httpServer: &http.Server{Addr: addr, Handler: r}}
}

func NewWithListener(listener net.Listener, lark *LarkHandler, requestLogger *log.Logger, ready ...ReadyReporter) *Server {
	r := newRouter(lark, requestLogger, firstReadyReporter(ready))
	return &Server{httpServer: &http.Server{Handler: r}, listener: listener}
}

func newRouter(lark *LarkHandler, requestLogger *log.Logger, ready ReadyReporter) http.Handler {
	if requestLogger == nil {
		requestLogger = log.New(io.Discard, "", log.LstdFlags)
	}
	if ready == nil {
		ready = func() (int, any) {
			return http.StatusOK, map[string]string{"status": "ready"}
		}
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: requestLogger, NoColor: true}))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		status, payload := ready()
		writeJSON(w, status, payload)
	})
	if lark != nil {
		r.Post("/lark/events", lark.Events)
		r.Post("/lark/actions", lark.Actions)
	}
	return r
}

func firstReadyReporter(reporters []ReadyReporter) ReadyReporter {
	if len(reporters) == 0 {
		return nil
	}
	return reporters[0]
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Serve() error {
	if s.listener != nil {
		return s.httpServer.Serve(s.listener)
	}
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
