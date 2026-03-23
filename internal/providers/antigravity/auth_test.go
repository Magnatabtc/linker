package antigravity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"linker/internal/platform"
	"linker/internal/providerkit"
)

func TestAuthenticateFallsBackToManualModeWhenCallbackPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve callback port: %v", err)
	}
	defer ln.Close()

	origPort := callbackPort
	callbackPort = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { callbackPort = origPort })

	var output strings.Builder
	ui := providerkit.Interactive{
		Env: platform.Environment{SSH: true, Headless: true},
		Prompt: func(prompt string, fallback string) string {
			return fallback
		},
		Printf: func(format string, args ...any) {
			fmt.Fprintf(&output, format, args...)
		},
		Println: func(args ...any) {
			fmt.Fprintln(&output, args...)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = authenticate(ctx, ui, nil)
	if err == nil {
		t.Fatal("expected authenticate to time out after entering manual callback mode")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if !strings.Contains(output.String(), "manual callback mode") {
		t.Fatalf("expected manual callback mode output, got %q", output.String())
	}
}
