package fsutil

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Saving must survive a concurrent reader of the target file (Windows refuses the rename then).
func TestReplaceWhileRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := os.WriteFile(path, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = os.ReadFile(path)
			}
		}
	}()
	for i := 0; i < 300; i++ {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Replace(tmp, path); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("save %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
}
