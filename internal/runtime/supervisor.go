package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"linker/internal/state"
)

type Supervisor struct {
	repo *state.Repository
	bind string
	port int
}

type Status struct {
	Running   bool
	PID       int
	StartedAt time.Time
}

func NewSupervisor(repo *state.Repository, bind string, port int) *Supervisor {
	return &Supervisor{repo: repo, bind: bind, port: port}
}

func (s *Supervisor) StartBackground(ctx context.Context, executable string) error {
	if status, _ := s.Status(); status.Running {
		return fmt.Errorf("linker is already running with pid %d", status.PID)
	}

	cmd := exec.CommandContext(ctx, executable, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := s.Status()
		if status.Running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("daemon did not start in time")
}

func (s *Supervisor) Stop() error {
	pid, err := s.repo.LoadPID()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !s.reachable() {
		return s.repo.RemovePID()
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Kill(); err != nil {
		return err
	}
	return s.repo.RemovePID()
}

func (s *Supervisor) Status() (Status, error) {
	pid, startedAt, err := s.repo.PIDInfo()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Status{}, nil
		}
		return Status{}, err
	}
	if !s.reachable() {
		_ = s.repo.RemovePID()
		return Status{}, nil
	}
	return Status{Running: pid > 0, PID: pid, StartedAt: startedAt}, nil
}

func (s *Supervisor) reachable() bool {
	if s.port == 0 {
		return false
	}
	address := net.JoinHostPort(s.bind, fmt.Sprintf("%d", s.port))
	conn, err := net.DialTimeout("tcp", address, 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
