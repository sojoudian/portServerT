package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	requestIDCounter uint64
	serverStartTime  = time.Now()
)

type Config struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

func loadConfig() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "10001"
	}

	return &Config{
		Port:            port,
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		requestID := atomic.AddUint64(&requestIDCounter, 1)
		
		log.Printf("[%d] Incoming request - Method: %s | Path: %s | RemoteAddr: %s | User-Agent: %s",
			requestID,
			r.Method,
			r.URL.Path,
			r.RemoteAddr,
			r.UserAgent(),
		)
		
		ctx := context.WithValue(r.Context(), "requestID", requestID)
		r = r.WithContext(ctx)
		
		next(w, r)
		
		duration := time.Since(start)
		log.Printf("[%d] Request completed - Duration: %v", requestID, duration)
	}
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next(w, r)
	}
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	requestID := r.Context().Value("requestID").(uint64)
	
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", fmt.Sprintf("%d", requestID))
	
	response := map[string]interface{}{
		"status":     "success",
		"message":    "Port 10001 is working fine",
		"timestamp":  time.Now().Format(time.RFC3339),
		"request_id": requestID,
		"path":       r.URL.Path,
		"method":     r.Method,
	}
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(serverStartTime)
	
	health := map[string]interface{}{
		"status":     "healthy",
		"uptime":     uptime.String(),
		"uptime_ms":  uptime.Milliseconds(),
		"timestamp":  time.Now().Format(time.RFC3339),
		"request_id": r.Context().Value("requestID"),
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(health)
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	requestID := r.Context().Value("requestID")
	
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", fmt.Sprintf("%d", requestID))
	
	response := map[string]interface{}{
		"status":     "error",
		"message":    "Resource not found",
		"path":       r.URL.Path,
		"request_id": requestID,
		"timestamp":  time.Now().Format(time.RFC3339),
	}
	
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(response)
}

func setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	
	mux.HandleFunc("/", corsMiddleware(loggingMiddleware(mainHandler)))
	mux.HandleFunc("/health", corsMiddleware(loggingMiddleware(healthHandler)))
	mux.HandleFunc("/healthz", corsMiddleware(loggingMiddleware(healthHandler)))
	
	return mux
}

func main() {
	config := loadConfig()
	
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	
	router := setupRoutes()
	
	srv := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      router,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}
	
	serverErrors := make(chan error, 1)
	
	go func() {
		log.Printf("Starting web server on http://localhost:%s ...", config.Port)
		log.Printf("Server configuration - ReadTimeout: %v | WriteTimeout: %v | IdleTimeout: %v",
			config.ReadTimeout, config.WriteTimeout, config.IdleTimeout)
		serverErrors <- srv.ListenAndServe()
	}()
	
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	
	select {
	case err := <-serverErrors:
		if err != nil {
			log.Fatalf("Server failed to start: %v", err)
		}
		
	case sig := <-shutdown:
		log.Printf("Received shutdown signal: %v", sig)
		
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		
		log.Println("Attempting graceful shutdown...")
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Could not gracefully shutdown the server: %v", err)
			srv.Close()
		}
		
		log.Println("Server stopped successfully")
	}
}