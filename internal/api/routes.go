package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/peiblow/eeapi/internal/api/handlers"
	"github.com/peiblow/eeapi/internal/auth"
	"github.com/peiblow/eeapi/internal/service"
)

func (s *Server) mount() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	r.Route("/", func(r chi.Router) {
		r.Use(auth.JWTMiddleware(s.clientPub))

		contractSvc := service.NewContractService(s.svm, s.db, s.priv, s.pub, s.locker)
		r.Post("/contracts/deploy", handlers.DeployHandler(contractSvc))
		r.Post("/contracts/{id}/execute", handlers.ExecHandler(contractSvc))
		r.Get("/trace/{contextId}", handlers.TraceHandler(contractSvc))
	})

	return r
}
