package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"linker/internal/platform"
	"linker/internal/providerkit"
	"linker/internal/providers/shared"
	"linker/internal/state"
)

const (
	authURL      = "https://auth.openai.com/oauth/authorize"
	tokenURL     = "https://auth.openai.com/oauth/token"
	clientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	callbackPort = 1455
	callbackPath = "/auth/callback"
	baseURL      = "https://api.openai.com"
)

func authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	if existing != nil && existing.AccessToken != "" && existing.RefreshToken != "" && strings.TrimSpace(existing.Email) != "" {
		if strings.EqualFold(strings.TrimSpace(ui.Prompt("Codex account already configured for "+existing.Email+". Keep current account? [Y/n]", "Y")), "n") {
		} else {
			return normalizeAuth(*existing), nil
		}
	}

	redirectURL := fmt.Sprintf("http://localhost:%d%s", callbackPort, callbackPath)
	stateToken, err := randomToken(24)
	if err != nil {
		return state.AccountAuth{}, err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return state.AccountAuth{}, err
	}
	challenge := pkceChallenge(verifier)

	listenerErr := make(chan error, 1)
	codeCh := make(chan string, 1)
	mux := http.NewServeMux()
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", callbackPort), Handler: mux}
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if errText := r.URL.Query().Get("error"); errText != "" {
			http.Error(w, errText, http.StatusBadRequest)
			select {
			case listenerErr <- fmt.Errorf("Codex OAuth failed: %s", errText):
			default:
			}
			return
		}
		if r.URL.Query().Get("state") != stateToken {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case listenerErr <- errors.New("Codex OAuth state mismatch"):
			default:
			}
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			select {
			case listenerErr <- errors.New("Codex OAuth callback missing code"):
			default:
			}
			return
		}
		_, _ = io.WriteString(w, "<html><body><h1>Linker authentication complete</h1><p>You can return to the terminal.</p></body></html>")
		select {
		case codeCh <- code:
		default:
		}
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case listenerErr <- err:
			default:
			}
		}
	}()
	defer server.Shutdown(context.Background())

	loginURL := buildAuthURL(redirectURL, stateToken, challenge)
	ui.Println()
	ui.Println("Authenticating Codex...")
	if shouldOpenBrowser(ui.Env) {
		ui.Println("  Opening browser for OpenAI OAuth...")
		if err := shared.OpenBrowser(loginURL); err != nil {
			ui.Printf("  Browser did not open automatically: %v\n", err)
		}
	} else {
		ui.Println("  Headless environment detected. Open the URL below in a browser.")
		ui.Printf("  If needed, forward localhost:%d from this machine before continuing.\n", callbackPort)
	}
	ui.Printf("  %s\n", loginURL)

	var manualTimer <-chan time.Time
	if ui.Env.SSH || ui.Env.Headless {
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		manualTimer = timer.C
	}
	timeout := time.NewTimer(5 * time.Minute)
	defer timeout.Stop()

	for {
		select {
		case code := <-codeCh:
			token, claims, err := exchangeCode(ctx, redirectURL, verifier, code)
			if err != nil {
				return state.AccountAuth{}, err
			}
			email := strings.TrimSpace(claims.Email)
			if email == "" {
				email = strings.TrimSpace(ui.Prompt("Codex account email", "openai"))
			}
			auth := state.AccountAuth{
				ID:               accountID("codex", email),
				Provider:         "codex",
				Email:            email,
				AuthType:         "oauth",
				AccessToken:      token.AccessToken,
				RefreshToken:     token.RefreshToken,
				ExpiresAt:        time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
				BaseURL:          baseURL,
				UpstreamType:     "openai",
				ClientID:         clientID,
				TokenURL:         tokenURL,
				RefreshURL:       tokenURL,
				AuthorizationURL: authURL,
				Metadata: map[string]string{
					"account_id": claims.Sub,
				},
			}
			return normalizeAuth(auth), nil
		case err := <-listenerErr:
			return state.AccountAuth{}, err
		case <-manualTimer:
			callbackValue := strings.TrimSpace(ui.Prompt("Paste the full Codex callback URL if the browser completed on another machine, or press Enter to keep waiting", ""))
			if callbackValue == "" {
				manualTimer = nil
				continue
			}
			code, err := parseManualCallback(callbackValue, stateToken)
			if err != nil {
				return state.AccountAuth{}, err
			}
			select {
			case codeCh <- code:
			default:
			}
			manualTimer = nil
		case <-ctx.Done():
			return state.AccountAuth{}, ctx.Err()
		case <-timeout.C:
			return state.AccountAuth{}, errors.New("timed out waiting for Codex OAuth callback")
		}
	}
}

func refresh(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	auth = normalizeAuth(auth)
	if auth.RefreshToken == "" || time.Until(auth.ExpiresAt) > 2*time.Minute {
		return auth, false, nil
	}
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", auth.RefreshToken)
	values.Set("scope", "openid profile email")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return auth, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := shared.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return auth, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return auth, false, fmt.Errorf("Codex token refresh failed: %s", strings.TrimSpace(string(body)))
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return auth, false, err
	}
	if token.AccessToken != "" {
		auth.AccessToken = token.AccessToken
	}
	if token.RefreshToken != "" {
		auth.RefreshToken = token.RefreshToken
	}
	if token.ExpiresIn > 0 {
		auth.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	if claims, err := parseIDToken(token.IDToken); err == nil {
		auth.Email = firstNonEmpty(claims.Email, auth.Email)
		if auth.Metadata == nil {
			auth.Metadata = map[string]string{}
		}
		auth.Metadata["account_id"] = claims.Sub
	}
	return auth, true, nil
}

func normalizeAuth(auth state.AccountAuth) state.AccountAuth {
	auth.Provider = "codex"
	auth.AuthType = "oauth"
	auth.BaseURL = baseURL
	auth.UpstreamType = "openai"
	auth.ClientID = clientID
	auth.TokenURL = tokenURL
	auth.RefreshURL = tokenURL
	auth.AuthorizationURL = authURL
	if auth.ID == "" && auth.Email != "" {
		auth.ID = accountID("codex", auth.Email)
	}
	return auth
}

func buildAuthURL(redirectURL string, stateToken string, challenge string) string {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("response_type", "code")
	values.Set("redirect_uri", redirectURL)
	values.Set("scope", "openid email profile offline_access")
	values.Set("state", stateToken)
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	values.Set("prompt", "login")
	values.Set("id_token_add_organizations", "true")
	values.Set("codex_cli_simplified_flow", "true")
	return authURL + "?" + values.Encode()
}

func exchangeCode(ctx context.Context, redirectURL string, verifier string, code string) (tokenResponse, idClaims, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("client_id", clientID)
	values.Set("code", code)
	values.Set("redirect_uri", redirectURL)
	values.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenResponse{}, idClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := shared.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return tokenResponse{}, idClaims{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return tokenResponse{}, idClaims{}, fmt.Errorf("Codex token exchange failed: %s", strings.TrimSpace(string(body)))
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return tokenResponse{}, idClaims{}, err
	}
	claims, err := parseIDToken(token.IDToken)
	return token, claims, err
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type idClaims struct {
	Email string `json:"email"`
	Sub   string `json:"sub"`
}

func parseIDToken(idToken string) (idClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return idClaims{}, errors.New("invalid id_token")
	}
	payload := parts[1]
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return idClaims{}, err
	}
	var claims idClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return idClaims{}, err
	}
	return claims, nil
}

func accountID(providerID string, email string) string {
	value := strings.ToLower(strings.TrimSpace(email))
	replacer := strings.NewReplacer("@", "_", ".", "_", "-", "_", " ", "_")
	return providerID + "_" + replacer.Replace(value)
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func shouldOpenBrowser(env platform.Environment) bool {
	return !env.SSH && !env.Headless
}

func parseManualCallback(raw string, expectedState string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid callback URL: %w", err)
	}
	if errText := parsed.Query().Get("error"); errText != "" {
		return "", fmt.Errorf("Codex OAuth failed: %s", errText)
	}
	if expectedState != "" && parsed.Query().Get("state") != expectedState {
		return "", errors.New("Codex OAuth state mismatch")
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	if code == "" {
		return "", errors.New("Codex callback URL did not contain a code")
	}
	return code, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
