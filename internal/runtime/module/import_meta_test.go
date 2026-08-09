package module

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

type importMetaEmbeddedResolver struct{}

func (importMetaEmbeddedResolver) ResolveEmbedded(string, string) (string, bool) { return "", false }
func (importMetaEmbeddedResolver) ModuleTypeOf(string) string                    { return "" }
func (importMetaEmbeddedResolver) LoadModule(string) (*bytecode.Module, error)   { return nil, nil }
func (importMetaEmbeddedResolver) LoadJSON(string) ([]byte, bool)                { return nil, false }

func TestCompiledImportMetaURLUsesBunBinaryMarker(t *testing.T) {
	loader := &Loader{embedded: importMetaEmbeddedResolver{}}
	metaFn, ok := loader.makeImportMetaFunc("src/main.ts").AsFunction()
	if !ok {
		t.Fatal("import meta value is not callable")
	}
	metaValue, err := metaFn.Call(nil)
	if err != nil {
		t.Fatalf("call import meta: %v", err)
	}
	meta, ok := metaValue.AsObject()
	if !ok {
		t.Fatal("import meta result is not an object")
	}
	url, err := meta.Get("url")
	if err != nil {
		t.Fatalf("get import.meta.url: %v", err)
	}
	if got, want := url.String(), "bun://~BUN/src/main.ts"; got != want {
		t.Fatalf("import.meta.url = %q, want %q", got, want)
	}
}
