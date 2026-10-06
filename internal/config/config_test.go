package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("ADMIN_LOGIN", "")
	t.Setenv("ADMIN_PASSWORD", "")
	cfg, err := Load()
	if err != nil || cfg.WorkerConcurrency != 5 || cfg.RewardPercent != 5 {
		t.Fatalf("defaults: %v %v", cfg, err)
	}
	if err = os.WriteFile(path, []byte("worker_concurrency: 7\nserver_port: 9000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil || cfg.WorkerConcurrency != 7 || cfg.ServerPort != 9000 {
		t.Fatalf("yaml: %v %v", cfg, err)
	}
	t.Setenv("WORKER_CONCURRENCY", "0")
	if _, err = Load(); err == nil {
		t.Fatal("zero concurrency accepted")
	}
	t.Setenv("WORKER_CONCURRENCY", "3")
	t.Setenv("ORDER_SERVICE_URL", "ftp://orders")
	if _, err = Load(); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
