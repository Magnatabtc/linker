package runtime

import (
	"testing"

	"linker/internal/state"
)

func TestStatusClearsStalePIDWhenPortIsDown(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	repo := state.NewRepository(layout)
	if err := repo.Init(); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	if err := repo.SavePID(999999); err != nil {
		t.Fatalf("save pid: %v", err)
	}

	supervisor := NewSupervisor(repo, "127.0.0.1", 6553)
	status, err := supervisor.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Running {
		t.Fatalf("expected stale pid to be treated as stopped")
	}
	if _, err := repo.LoadPID(); err == nil {
		t.Fatalf("expected stale pid file to be removed")
	}
}
