package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"linker/internal/catalog"
	"linker/internal/compat"
	"linker/internal/config"
	"linker/internal/logging"
	"linker/internal/provider"
	"linker/internal/providers/shared"
	"linker/internal/router"
	"linker/internal/state"
)

type Server struct {
	cfg       config.Config
	repo      *state.Repository
	store     *config.Store
	providers *provider.Registry
	catalog   *catalog.Service
	logger    *logging.Logger
	mu        sync.Mutex
}

func New(cfg config.Config, repo *state.Repository, store *config.Store, providers *provider.Registry, catalog *catalog.Service, logger *logging.Logger) *Server {
	return &Server{cfg: cfg, repo: repo, store: store, providers: providers, catalog: catalog, logger: logger}
}

func (s *Server) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ready")) })
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/messages", s.handleAnthropic)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAI)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.cfg.Bind, s.cfg.Port),
		Handler: mux,
	}
	if err := s.repo.SavePID(os.Getpid()); err != nil {
		return err
	}
	defer s.repo.RemovePID()

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	s.logger.Println("server listening on", server.Addr)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	registry, _ := s.repo.LoadRegistry()
	type model struct {
		ID string `json:"id"`
	}
	payload := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list"}
	add := func(id string) {
		for _, item := range payload.Data {
			if item.ID == id {
				return
			}
		}
		payload.Data = append(payload.Data, model{ID: id})
	}
	add("claude-opus-4-20250514")
	add("claude-sonnet-4-20250514")
	add("claude-haiku-4-20250514")
	for _, mapping := range []config.ModelTarget{s.cfg.ModelMapping.Default, s.cfg.ModelMapping.Opus, s.cfg.ModelMapping.Sonnet, s.cfg.ModelMapping.Haiku} {
		if mapping.Model != "" {
			add(mapping.Model)
		}
	}
	for _, entries := range registry.Entries {
		for _, item := range entries {
			add(item.Name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleAnthropic(w http.ResponseWriter, r *http.Request) {
	if !hasLocalAuth(r) {
		http.Error(w, "missing local auth token", http.StatusUnauthorized)
		return
	}
	req, err := compat.ParseAnthropicRequest(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		if handled, err := s.tryNativeAnthropicStream(r.Context(), req, w); handled {
			if err != nil {
				writeProxyError(w, err)
			}
			return
		}
		resp, err := s.invoke(r.Context(), req)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		if err := compat.WriteAnthropicStream(w, resp); err != nil {
			writeProxyError(w, err)
		}
		return
	}
	resp, err := s.invoke(r.Context(), req)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := compat.WriteAnthropicResponse(w, resp); err != nil {
		writeProxyError(w, err)
	}
}

func (s *Server) handleOpenAI(w http.ResponseWriter, r *http.Request) {
	if !hasLocalAuth(r) {
		http.Error(w, "missing local auth token", http.StatusUnauthorized)
		return
	}
	req, err := compat.ParseOpenAIRequest(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		if handled, err := s.tryNativeOpenAIStream(r.Context(), req, w); handled {
			if err != nil {
				writeProxyError(w, err)
			}
			return
		}
		resp, err := s.invoke(r.Context(), req)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		if err := compat.WriteOpenAIStream(w, resp); err != nil {
			writeProxyError(w, err)
		}
		return
	}
	resp, err := s.invoke(r.Context(), req)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := compat.WriteOpenAIResponse(w, resp); err != nil {
		writeProxyError(w, err)
	}
}

func (s *Server) invoke(ctx context.Context, req compat.NormalizedRequest) (compat.NormalizedResponse, error) {
	registry, _ := s.repo.LoadRegistry()
	targets, err := router.ResolveCandidates(s.cfg, registry, s.providers, req.Model)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	var lastErr error
	for _, target := range targets {
		authPath := target.AuthFile
		if !filepath.IsAbs(authPath) {
			authPath = filepath.Join(s.repo.Layout().Root, authPath)
			if !strings.Contains(authPath, s.repo.Layout().AuthDir) {
				authPath = filepath.Join(s.repo.Layout().AuthDir, filepath.Base(target.AuthFile))
			}
		}
		auth, err := s.repo.LoadAuth(authPath)
		if err != nil {
			lastErr = err
			continue
		}
		req.Model = target.Model
		resp, updatedAuth, err := s.providers.Invoke(ctx, auth, req)
		if updatedAuth.Provider != "" {
			_, _ = s.repo.SaveAuth(updatedAuth)
		}
		if err == nil {
			s.rotateAccountCursor(target)
			if resp.Model == "" {
				resp.Model = target.Model
			}
			return resp, nil
		}
		lastErr = err
		if !shouldTryNextAccount(err) || target.AccountID == "" {
			return compat.NormalizedResponse{}, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no route candidates available")
	}
	return compat.NormalizedResponse{}, lastErr
}

func shouldTryNextAccount(err error) bool {
	var statusErr *shared.HTTPError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden || statusErr.StatusCode == http.StatusTooManyRequests
}

func (s *Server) rotateAccountCursor(target router.Target) {
	if target.AccountID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	providerCfg, ok := s.cfg.Providers[target.Provider]
	if !ok || len(providerCfg.Accounts) < 2 {
		return
	}
	currentIndex := -1
	for i, account := range providerCfg.Accounts {
		if account.ID == target.AccountID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return
	}
	nextIndex := (currentIndex + 1) % len(providerCfg.Accounts)
	providerCfg.ActiveAccountID = providerCfg.Accounts[nextIndex].ID
	for i := range providerCfg.Accounts {
		providerCfg.Accounts[i].Active = i == nextIndex
	}
	s.cfg.Providers[target.Provider] = providerCfg
	_ = s.store.Save(s.cfg)
}

type anthropicNativeStreamer interface {
	StreamAnthropic(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest, w io.Writer) (state.AccountAuth, error)
}

type openAINativeStreamer interface {
	StreamOpenAI(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest, w io.Writer) (state.AccountAuth, error)
}

func (s *Server) tryNativeAnthropicStream(ctx context.Context, req compat.NormalizedRequest, w io.Writer) (bool, error) {
	targets, err := s.resolveTargets(req.Model)
	if err != nil {
		return false, err
	}
	handled := false
	var lastErr error
	for _, target := range targets {
		auth, driver, err := s.loadTargetDriver(target)
		if err != nil {
			lastErr = err
			continue
		}
		streamer, ok := driver.(anthropicNativeStreamer)
		if !ok {
			continue
		}
		handled = true
		req.Model = target.Model
		updatedAuth, err := streamer.StreamAnthropic(ctx, auth, req, w)
		if updatedAuth.Provider != "" {
			_, _ = s.repo.SaveAuth(updatedAuth)
		}
		if err == nil {
			s.rotateAccountCursor(target)
			return true, nil
		}
		lastErr = err
		if !shouldTryNextAccount(err) || target.AccountID == "" {
			return true, err
		}
	}
	return handled, lastErr
}

func (s *Server) tryNativeOpenAIStream(ctx context.Context, req compat.NormalizedRequest, w io.Writer) (bool, error) {
	targets, err := s.resolveTargets(req.Model)
	if err != nil {
		return false, err
	}
	handled := false
	var lastErr error
	for _, target := range targets {
		auth, driver, err := s.loadTargetDriver(target)
		if err != nil {
			lastErr = err
			continue
		}
		streamer, ok := driver.(openAINativeStreamer)
		if !ok {
			continue
		}
		handled = true
		req.Model = target.Model
		updatedAuth, err := streamer.StreamOpenAI(ctx, auth, req, w)
		if updatedAuth.Provider != "" {
			_, _ = s.repo.SaveAuth(updatedAuth)
		}
		if err == nil {
			s.rotateAccountCursor(target)
			return true, nil
		}
		lastErr = err
		if !shouldTryNextAccount(err) || target.AccountID == "" {
			return true, err
		}
	}
	return handled, lastErr
}

func (s *Server) resolveTargets(model string) ([]router.Target, error) {
	registry, _ := s.repo.LoadRegistry()
	return router.ResolveCandidates(s.cfg, registry, s.providers, model)
}

func (s *Server) loadTargetDriver(target router.Target) (state.AccountAuth, any, error) {
	authPath := target.AuthFile
	if !filepath.IsAbs(authPath) {
		authPath = filepath.Join(s.repo.Layout().Root, authPath)
		if !strings.Contains(authPath, s.repo.Layout().AuthDir) {
			authPath = filepath.Join(s.repo.Layout().AuthDir, filepath.Base(target.AuthFile))
		}
	}
	auth, err := s.repo.LoadAuth(authPath)
	if err != nil {
		return state.AccountAuth{}, nil, err
	}
	driver, ok := s.providers.Driver(target.Provider)
	if !ok {
		return state.AccountAuth{}, nil, fmt.Errorf("unknown provider %q", target.Provider)
	}
	return auth, driver, nil
}

func writeProxyError(w http.ResponseWriter, err error) {
	var statusErr *shared.HTTPError
	if errors.As(err, &statusErr) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusErr.StatusCode)
		body := strings.TrimSpace(statusErr.Body)
		if json.Valid([]byte(body)) {
			_, _ = w.Write([]byte(body))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":    "proxy_error",
				"message": body,
			},
		})
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func hasLocalAuth(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isLoopbackHost(host) {
		return false
	}
	if strings.TrimSpace(r.Header.Get("x-api-key")) != "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.HasPrefix(strings.ToLower(auth), "bearer ") && strings.TrimSpace(auth[len("Bearer "):]) != ""
}

func isLoopbackHost(host string) bool {
	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
