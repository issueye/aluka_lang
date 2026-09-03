package parser

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

func TestTemplateInterpolationInheritsAwaitContext(t *testing.T) {
	prog, err := ParseModule(`
export async function load(response) {
  return ` + "`status: ${truncate(await response.text())}`" + `;
}
const top = ` + "`value: ${await read()}`" + `;
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ast.HasTopLevelAwait(prog) {
		t.Fatal("expected top-level await inside template interpolation")
	}
}
