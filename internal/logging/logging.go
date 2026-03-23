package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

type Logger struct {
	*log.Logger
	file *os.File
}

func Open(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	writer := io.MultiWriter(os.Stdout, file)
	return &Logger{
		Logger: log.New(writer, "linker ", log.LstdFlags|log.Lmicroseconds),
		file:   file,
	}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) PrintKV(message string, kv map[string]any) {
	if len(kv) == 0 {
		l.Println(message)
		return
	}
	l.Printf("%s %s\n", message, fmt.Sprint(kv))
}
