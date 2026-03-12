package gemini

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
	callbackPort     = 8085
	callbackPath     = "/oauth2callback"
	authorizationURL = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL         = "https://oauth2.googleapis.com/token"
	userInfoURL      = "https://www.googleapis.com/oauth2/v1/userinfo?alt=json"
	openAIBaseURL    = "https://generativelanguage.googleapis.com/v1beta/openai"
	modelsURL        = "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000"
)

var (
	clientID     = strings.Join([]string{"681255809395", "oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"}, "-")
	clientSecret = strings.Join([]string{"GOCSPX", "4uHgMPm", "1o7Sk", "geV6Cu5clXFsxl"}, "-")
)

var oauthScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

func authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	if existing != nil && existing.AccessToken != "" && existing.RefreshToken != "" && strings.TrimSpace(existing.Email) != "" {
		if strings.EqualFold(strings.TrimSpace(ui.Prompt("Gemini CLI account already configured for "+existing.Email+". Keep current account? [Y/n]", "Y")), "n") {
		} else {
			return normalizeAuth(*existing), nil
		}
	}

	redirectURL := fmt.Sprintf("http://localhost:%d%s", callbackPort, callbackPath)
	listenerErr := make(chan error, 1)
	codeCh := make(chan string, 1)
	stateToken, err := randomToken(24)
	if err != nil {
		return state.AccountAuth{}, err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return state.AccountAuth{}, err
	}
	challenge := pkceChallenge(verifier)

	mux := http.NewServeMux()
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", callbackPort), Handler: mux}
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if errText := r.URL.Query().Get("error"); errText != "" {
			http.Error(w, errText, http.StatusBadRequest)
			select {
			case listenerErr <- fmt.Errorf("Gemini OAuth failed: %s", errText):
			default:
			}
			return
		}
		if r.URL.Query().Get("state") != stateToken {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case listenerErr <- errors.New("Gemini OAuth state mismatch"):
			default:
			}
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			select {
			case listenerErr <- errors.New("Gemini OAuth callback missing code"):
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

	authURL := buildAuthURL(clientID, redirectURL, stateToken, challenge)
	ui.Println()
	ui.Println("Authenticating with Gemini CLI...")
	if shouldOpenBrowser(ui.Env) {
		ui.Println("  Opening browser for Google OAuth...")
		if err := shared.OpenBrowser(authURL); err != nil {
			ui.Printf("  Browser did not open automatically: %v\n", err)
		}
	} else {
		ui.Println("  Headless environment detected. Open the URL below in a browser.")
		ui.Printf("  If needed, forward localhost:%d from this machine before continuing.\n", callbackPort)
	}
	ui.Printf("  %s\n", authURL)
	ui.Println("  Waiting for Google callback...")

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
			token, err := exchangeCode(ctx, redirectURL, verifier, code)
			if err != nil {
				return state.AccountAuth{}, err
			}
			email, err := fetchEmail(ctx, token.AccessToken)
			if err != nil {
				return state.AccountAuth{}, err
			}
			auth := state.AccountAuth{
				ID:               accountID("gemini-cli", email),
				Provider:         "gemini-cli",
				Email:            email,
				AuthType:         "oauth",
				AccessToken:      token.AccessToken,
				RefreshToken:     token.RefreshToken,
				ExpiresAt:        time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
				BaseURL:          openAIBaseURL,
				UpstreamType:     "openai",
				ClientID:         clientID,
				ClientSecret:     clientSecret,
				TokenURL:         tokenURL,
				RefreshURL:       tokenURL,
				AuthorizationURL: authorizationURL,
			}
			ui.Printf("  %s authenticated successfully.\n", email)
			return normalizeAuth(auth), nil
		case err := <-listenerErr:
			return state.AccountAuth{}, err
		case <-manualTimer:
			callbackValue := strings.TrimSpace(ui.Prompt("Paste the full Gemini callback URL if the browser completed on another machine, or press Enter to keep waiting", ""))
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
			return state.AccountAuth{}, errors.New("timed out waiting for Gemini OAuth callback")
		}
	}
}

func refresh(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	auth = normalizeAuth(auth)
	if auth.RefreshToken == "" || time.Until(auth.ExpiresAt) > 2*time.Minute {
		return auth, false, nil
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("refresh_token", auth.RefreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return auth, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := shared.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return auth, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return auth, false, fmt.Errorf("Gemini token refresh failed: %s", strings.TrimSpace(string(body)))
	}
	var token oauthToken
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
	return auth, true, nil
}

func normalizeAuth(auth state.AccountAuth) state.AccountAuth {
	auth.Provider = "gemini-cli"
	auth.AuthType = "oauth"
	auth.BaseURL = openAIBaseURL
	auth.UpstreamType = "openai"
	auth.ClientID = clientID
	auth.ClientSecret = clientSecret
	auth.TokenURL = tokenURL
	auth.RefreshURL = tokenURL
	auth.AuthorizationURL = authorizationURL
	if auth.ID == "" && auth.Email != "" {
		auth.ID = accountID("gemini-cli", auth.Email)
	}
	return auth
}

func buildAuthURL(clientID string, redirectURL string, stateToken string, challenge string) string {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURL)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(oauthScopes, " "))
	values.Set("access_type", "offline")
	values.Set("prompt", "consent")
	values.Set("state", stateToken)
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	return authorizationURL + "?" + values.Encode()
}

func exchangeCode(ctx context.Context, redirectURL string, verifier string, code string) (oauthToken, error) {
	values := url.Values{}
	values.Set("code", code)
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("redirect_uri", redirectURL)
	values.Set("grant_type", "authorization_code")
	values.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return oauthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := shared.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return oauthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return oauthToken{}, fmt.Errorf("Gemini token exchange failed: %s", strings.TrimSpace(string(body)))
	}
	var token oauthToken
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthToken{}, err
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return oauthToken{}, errors.New("Gemini token exchange returned incomplete credentials")
	}
	return token, nil
}

func fetchEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := shared.HTTPClient(15 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("Gemini userinfo failed: %s", strings.TrimSpace(string(body)))
	}
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Email) == "" {
		return "", errors.New("Gemini userinfo response missing email")
	}
	return payload.Email, nil
}

type oauthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
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
		return "", fmt.Errorf("Gemini OAuth failed: %s", errText)
	}
	if expectedState != "" && parsed.Query().Get("state") != expectedState {
		return "", errors.New("Gemini OAuth state mismatch")
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	if code == "" {
		return "", errors.New("Gemini callback URL did not contain a code")
	}
	return code, nil
}
