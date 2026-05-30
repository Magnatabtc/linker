# CLAUDE_SUG.md — linker
> Revisão de segurança e qualidade — 2026-05-14

## 🔴 CRÍTICO

### 1. Bearer Token Aceita Qualquer Valor (Bypass de Autenticação)
**Arquivo:** `internal/api/server.go`, linhas 386–407

```go
// PROBLEMA: qualquer string não-vazia é aceita como autenticação válida
func hasLocalAuth(r *http.Request) bool {
    authHeader := r.Header.Get("Authorization")
    return strings.HasPrefix(authHeader, "Bearer ") // não valida o token!
}
```

**Exploit:**
```bash
curl -H "Authorization: Bearer anything" http://localhost:6767/v1/messages
# Retorna 200 com acesso total
```

**Fix:**
```go
import "crypto/subtle"

var localToken = generateTokenOnStartup() // token aleatório gerado no startup

func hasLocalAuth(r *http.Request) bool {
    authHeader := r.Header.Get("Authorization")
    if !strings.HasPrefix(authHeader, "Bearer ") {
        return false
    }
    token := strings.TrimPrefix(authHeader, "Bearer ")
    expected := []byte(localToken)
    actual := []byte(token)
    if len(expected) != len(actual) {
        return false
    }
    return subtle.ConstantTimeCompare(expected, actual) == 1
}

func generateTokenOnStartup() string {
    b := make([]byte, 32)
    rand.Read(b)
    return base64.URLEncoding.EncodeToString(b)
}
```

---

## 🟠 ALTO

### 2. Ausência de Rate Limiting em Endpoints Públicos
**Impacto:** Sem rate limiting, endpoints ficam expostos a brute force e abuso de recursos. Combinado com o bypass de auth acima, qualquer cliente pode fazer requisições ilimitadas.

**Fix:**
```go
import "golang.org/x/time/rate"

limiter := rate.NewLimiter(rate.Every(time.Second), 10) // 10 req/s

func rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !limiter.Allow() {
            http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

## 🟡 MÉDIO

### 3. Goroutines sem Context Cancellation
**Problema:** Goroutines de background sem `context.Context` não são canceladas no shutdown, causando goroutine leaks.

**Fix:**
```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
go processMessages(ctx, ch) // propagar ctx para todas goroutines

func processMessages(ctx context.Context, ch <-chan Message) {
    for {
        select {
        case <-ctx.Done():
            return
        case msg := <-ch:
            handle(msg)
        }
    }
}
```

### 4. Ausência de CORS Restritivo
**Problema:** CORS configurado permissivamente (`*`) permite requisições cross-origin arbitrárias.

**Fix:**
```go
corsMiddleware := cors.New(cors.Options{
    AllowedOrigins: []string{"http://localhost:3000"}, // origens explícitas
    AllowedMethods: []string{"GET", "POST"},
    AllowCredentials: true,
})
```
