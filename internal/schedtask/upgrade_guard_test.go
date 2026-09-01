package schedtask

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreDoesNotOverwriteFutureVersions(t *testing.T) {
	for _, name := range []string{"tasks", "runs"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tasksPath := filepath.Join(dir, "tasks.json")
			runsPath := filepath.Join(dir, "runs.json")
			path := tasksPath
			if name == "runs" {
				path = runsPath
			}
			original := []byte(`{"version":99,"future":"keep"}`)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			store := NewStore(tasksPath, runsPath)
			var err error
			if name == "tasks" {
				err = store.SaveTasks(nil)
			} else {
				err = store.SaveRuns(nil)
			}
			if err == nil {
				t.Fatal("future task format must reject writes")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(original) {
				t.Fatal("future task file was overwritten")
			}
		})
	}
}
