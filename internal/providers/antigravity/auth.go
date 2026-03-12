package antigravity

import (
	"bytes"
	"context"
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
	callbackPort     = 51121
	callbackPath     = "/oauth-callback"
	authEndpoint     = "https://accounts.google.com/o/oauth2/auth"
	tokenEndpoint    = "https://oauth2.googleapis.com/token"
	userInfoEndpoint = "https://www.googleapis.com/oauth2/v1/userinfo?alt=json"
	apiEndpoint      = "https://cloudcode-pa.googleapis.com"
	apiVersion       = "v1internal"
	googAPIClient    = "google-cloud-sdk vscode_cloudshelleditor/0.1"
)

var (
	clientID     = strings.Join([]string{"1071006060591", "tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"}, "-")
	clientSecret = strings.Join([]string{"GOCSPX", "K58FWR486LdLJ1mLB8sXC4z6qDAf"}, "-")
)

var scopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

func authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	if existing != nil && existing.AccessToken != "" && existing.RefreshToken != "" && strings.TrimSpace(existing.Email) != "" && strings.TrimSpace(existing.ProjectID) != "" {
		if strings.EqualFold(strings.TrimSpace(ui.Prompt("Antigravity account already configured for "+existing.Email+". Keep current account? [Y/n]", "Y")), "n") {
		} else {
			return normalizeAuth(*existing), nil
		}
	}

	redirectURL := fmt.Sprintf("http://localhost:%d%s", callbackPort, callbackPath)
	listenerErr := make(chan error, 1)
	codeCh := make(chan string, 1)
	stateToken := fmt.Sprintf("linker-antigravity-%d", time.Now().UnixNano())

	mux := http.NewServeMux()
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", callbackPort), Handler: mux}
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if errText := r.URL.Query().Get("error"); errText != "" {
			http.Error(w, errText, http.StatusBadRequest)
			select {
			case listenerErr <- fmt.Errorf("Antigravity OAuth failed: %s", errText):
			default:
			}
			return
		}
		if r.URL.Query().Get("state") != stateToken {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case listenerErr <- errors.New("Antigravity OAuth state mismatch"):
			default:
			}
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			select {
			case listenerErr <- errors.New("Antigravity OAuth callback missing code"):
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

	authURL := buildAuthURL(redirectURL, stateToken)
	ui.Println()
	ui.Println("Authenticating with Antigravity...")
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
			token, err := exchangeCode(ctx, redirectURL, code)
			if err != nil {
				return state.AccountAuth{}, err
			}
			email, err := fetchEmail(ctx, token.AccessToken)
			if err != nil {
				return state.AccountAuth{}, err
			}
			projectID, err := fetchProjectID(ctx, token.AccessToken)
			if err != nil {
				return state.AccountAuth{}, err
			}
			auth := state.AccountAuth{
				ID:               accountID("antigravity", email),
				Provider:         "antigravity",
				Email:            email,
				AuthType:         "oauth",
				AccessToken:      token.AccessToken,
				RefreshToken:     token.RefreshToken,
				ExpiresAt:        time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
				BaseURL:          apiEndpoint,
				UpstreamType:     "antigravity",
				ClientID:         clientID,
				ClientSecret:     clientSecret,
				TokenURL:         tokenEndpoint,
				RefreshURL:       tokenEndpoint,
				AuthorizationURL: authEndpoint,
				ProjectID:        projectID,
			}
			ui.Printf("  %s authenticated successfully.\n", email)
			return normalizeAuth(auth), nil
		case err := <-listenerErr:
			return state.AccountAuth{}, err
		case <-manualTimer:
			callbackValue := strings.TrimSpace(ui.Prompt("Paste the full Antigravity callback URL if the browser completed on another machine, or press Enter to keep waiting", ""))
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
			return state.AccountAuth{}, errors.New("timed out waiting for Antigravity OAuth callback")
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(values.Encode()))
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
		return auth, false, fmt.Errorf("Antigravity token refresh failed: %s", strings.TrimSpace(string(body)))
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
	auth.Provider = "antigravity"
	auth.AuthType = "oauth"
	auth.BaseURL = apiEndpoint
	auth.UpstreamType = "antigravity"
	auth.ClientID = clientID
	auth.ClientSecret = clientSecret
	auth.TokenURL = tokenEndpoint
	auth.RefreshURL = tokenEndpoint
	auth.AuthorizationURL = authEndpoint
	if auth.ID == "" && auth.Email != "" {
		auth.ID = accountID("antigravity", auth.Email)
	}
	return auth
}

func buildAuthURL(redirectURL string, stateToken string) string {
	values := url.Values{}
	values.Set("access_type", "offline")
	values.Set("client_id", clientID)
	values.Set("prompt", "consent")
	values.Set("redirect_uri", redirectURL)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(scopes, " "))
	values.Set("state", stateToken)
	return authEndpoint + "?" + values.Encode()
}

func exchangeCode(ctx context.Context, redirectURL string, code string) (oauthToken, error) {
	values := url.Values{}
	values.Set("code", code)
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("redirect_uri", redirectURL)
	values.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(values.Encode()))
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
		return oauthToken{}, fmt.Errorf("Antigravity token exchange failed: %s", strings.TrimSpace(string(body)))
	}
	var token oauthToken
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthToken{}, err
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return oauthToken{}, errors.New("Antigravity token exchange returned incomplete credentials")
	}
	return token, nil
}

func fetchEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoEndpoint, nil)
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
		return "", fmt.Errorf("Antigravity userinfo failed: %s", strings.TrimSpace(string(body)))
	}
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Email) == "" {
		return "", errors.New("Antigravity userinfo response missing email")
	}
	return payload.Email, nil
}

func fetchProjectID(ctx context.Context, accessToken string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]string{
			"ideType":    "ANTIGRAVITY",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s:loadCodeAssist", apiEndpoint, apiVersion), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	applyAPIHeaders(req, accessToken, platform.Environment{OS: "linux", Arch: "amd64"})
	resp, err := shared.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("Antigravity loadCodeAssist failed: %s", strings.TrimSpace(string(data)))
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if projectID := parseProjectID(payload["cloudaicompanionProject"]); projectID != "" {
		return projectID, nil
	}
	tierID := "legacy-tier"
	if allowedTiers, ok := payload["allowedTiers"].([]any); ok {
		for _, raw := range allowedTiers {
			tier, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if isDefault, _ := tier["isDefault"].(bool); isDefault {
				if id, _ := tier["id"].(string); strings.TrimSpace(id) != "" {
					tierID = strings.TrimSpace(id)
					break
				}
			}
		}
	}
	return onboardUser(ctx, accessToken, tierID)
}

func onboardUser(ctx context.Context, accessToken string, tierID string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"tierId": tierID,
		"metadata": map[string]string{
			"ideType":    "ANTIGRAVITY",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	})
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s:onboardUser", apiEndpoint, apiVersion), bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		applyAPIHeaders(req, accessToken, platform.Environment{OS: "linux", Arch: "amd64"})
		resp, err := shared.HTTPClient(30 * time.Second).Do(req)
		if err != nil {
			return "", err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("Antigravity onboardUser failed: %s", strings.TrimSpace(string(data)))
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return "", err
		}
		if done, _ := payload["done"].(bool); !done {
			time.Sleep(2 * time.Second)
			continue
		}
		if responseMap, ok := payload["response"].(map[string]any); ok {
			if projectID := parseProjectID(responseMap["cloudaicompanionProject"]); projectID != "" {
				return projectID, nil
			}
		}
	}
	return "", errors.New("Antigravity onboarding did not return a project id")
}

func parseProjectID(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		id, _ := value["id"].(string)
		return strings.TrimSpace(id)
	default:
		return ""
	}
}

func accountID(providerID string, email string) string {
	value := strings.ToLower(strings.TrimSpace(email))
	replacer := strings.NewReplacer("@", "_", ".", "_", "-", "_", " ", "_")
	return providerID + "_" + replacer.Replace(value)
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
		return "", fmt.Errorf("Antigravity OAuth failed: %s", errText)
	}
	if expectedState != "" && parsed.Query().Get("state") != expectedState {
		return "", errors.New("Antigravity OAuth state mismatch")
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	if code == "" {
		return "", errors.New("Antigravity callback URL did not contain a code")
	}
	return code, nil
}

type oauthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}
