package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Server provides an http.Server
type Server struct {
	l *zap.Logger
	*http.Server
}

// NewServer creates and configures a server serving all application routes.
//
// The server implements a gracefull shutdown and utilizes zap.Logger for logging purposes.
// chi.Mux is used for registering some convenient middlewares and easy configuration of
// routes using different http verbs.
func NewServer(listenAddr string) (*Server, error) {
	logger, err := zap.NewDevelopment(zap.AddStacktrace(zapcore.FatalLevel))
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Configure API")
	api := newAPI(logger)

	errorLog, _ := zap.NewStdLogAt(logger, zap.ErrorLevel)
	srv := http.Server{
		Addr:         listenAddr,
		Handler:      api,
		ErrorLog:     errorLog,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	return &Server{logger, &srv}, nil
}

// Start runs ListenAndServe on the http.Server with graceful shutdown
func (srv *Server) Start() {
	srv.l.Info("Starting server...")
	defer srv.l.Sync()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srv.l.Fatal("Could not listen on", zap.String("addr", srv.Addr), zap.Error(err))
		}
	}()
	srv.l.Info("Server is ready to handle requests", zap.String("addr", srv.Addr))
	srv.gracefullShutdown()
}

func newAPI(logger *zap.Logger) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(zapLogger(logger))
	r.Use(middleware.StripSlashes)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})

	// register more routes over here...

	logRoutes(r, logger)

	return r
}

func zapLogger(l *zap.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			t1 := time.Now()
			defer func() {
				l.Info("Served",
					zap.String("proto", r.Proto),
					zap.String("path", r.URL.Path),
					zap.Duration("lat", time.Since(t1)),
					zap.Int("status", ww.Status()),
					zap.Int("size", ww.BytesWritten()),
					zap.String("reqId", middleware.GetReqID(r.Context())),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func logRoutes(r *chi.Mux, logger *zap.Logger) {
	if err := chi.Walk(r, zapPrintRoute(logger)); err != nil {
		logger.Error("Failed to walk routes:", zap.Error(err))
	}
}

func zapPrintRoute(logger *zap.Logger) chi.WalkFunc {
	return func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		route = strings.Replace(route, "/*/", "/", -1)
		logger.Debug("Registering route", zap.String("method", method), zap.String("route", route))
		return nil
	}
}

// Close implements the Closer interface to handle a graceful shutdown of the server
func (srv *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srv.SetKeepAlivesEnabled(false)
	if err := srv.Shutdown(ctx); err != nil {
		srv.l.Fatal("Could not gracefully shutdown the server", zap.Error(err))
	}
	srv.l.Info("Server stopped")
	return nil
}

func (srv *Server) gracefullShutdown() {
	defer srv.Close()
	quit := make(chan os.Signal, 1)

	signal.Notify(quit, os.Interrupt)
	sig := <-quit
	srv.l.Info("Server is shutting down", zap.String("reason", sig.String()))
}
