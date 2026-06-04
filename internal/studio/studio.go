package studio

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type Studio struct {
	db   *DB
	otlp *OTLPReceiver
	api  *APIServer
}

func NewStudio(dbPath string) (*Studio, error) {
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, err
	}

	return &Studio{
		db:   db,
		otlp: NewOTLPReceiver(db),
		api:  NewAPIServer(db),
	}, nil
}

func (s *Studio) Start(ctx context.Context, otlpGRPCPort, otlpHTTPPort, apiPort int) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	// Start OTLP gRPC Receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.otlp.StartGRPC(otlpGRPCPort); err != nil {
			errCh <- fmt.Errorf("OTLP gRPC receiver failed: %w", err)
		}
	}()

	// Start OTLP HTTP Receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.otlp.StartHTTP(otlpHTTPPort); err != nil {
			errCh <- fmt.Errorf("OTLP HTTP receiver failed: %w", err)
		}
	}()

	// Start API Server
	wg.Add(1)
	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		s.api.RegisterHandlers(mux)

		// Serve static assets
		staticFS := StaticAssets()
		fileServer := http.FileServer(http.FS(staticFS))

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// If it's an API call, let the mux handle it
			if strings.HasPrefix(r.URL.Path, "/api") {
				mux.ServeHTTP(w, r)
				return
			}

			// Check if file exists in static FS
			f, err := staticFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}

			// Fallback to index.html for SPA routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		})

		fmt.Printf("Studio API listening on :%d\n", apiPort)
		server := &http.Server{Addr: fmt.Sprintf(":%d", apiPort), Handler: handler}

		go func() {
			<-ctx.Done()
			server.Shutdown(context.Background())
		}()

		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("API server failed: %w", err)
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		s.db.Close()
		return nil
	case err := <-errCh:
		s.db.Close()
		return err
	}
}
