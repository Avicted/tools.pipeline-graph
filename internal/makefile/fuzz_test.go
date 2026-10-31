package makefile

import (
	"os"
	"path/filepath"
	"testing"
)

// Makefiles come from untrusted repositories. Loading one must not panic, however broken it is.
// `go test` runs the seeds; to search for more: go test ./internal/makefile -fuzz=FuzzLoad -fuzztime=1m
func FuzzLoad(f *testing.F) {
	for _, seed := range []string{
		"include mk/common.mk\nA := $(B)\nall: $(A) test # c\n\tgo build \\\n\t  ./cmd\n",
		".PHONY: a b\na b: c | d\n\t@echo $@ $< $^\nifeq ($(X),1)\nc:\nelse\nc: ;\nendif\n",
		"define X\nbody\nendef\n%.o: %.c\n\t$(CC) $<\n-include missing.mk\n",
		"$(", "$(foo", "${", "a:", ":", "\t", "include", "include $(", "define", "endef", "a: b: c", "x = $(x)", "x := $(x) $(y)\ny = $(x)",
	} {
		f.Add(seed)
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, src string) {
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := Load(dir, ".", "", nil)
		if err != nil {
			return
		}
		for _, r := range m.Rules {
			_ = m.ExpandRecipe(r, "echo $@ $(CC)")
		}
		_ = m.Expand(src)
	})
}
