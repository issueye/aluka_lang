package project

import "testing"

func TestMergeDefinesUserOverridesBuiltin(t *testing.T) {
	got := mergeDefines(Options{
		Defines: map[string]string{
			"process.env.NODE_ENV": `"development"`,
			"__VUE_OPTIONS_API__":  "false",
		},
	})
	if got["process.env.NODE_ENV"] != `"development"` {
		t.Fatalf("NODE_ENV = %q, want user development", got["process.env.NODE_ENV"])
	}
	if got["__VUE_OPTIONS_API__"] != "false" {
		t.Fatalf("OPTIONS_API = %q, want false", got["__VUE_OPTIONS_API__"])
	}
	if got["__VUE_PROD_DEVTOOLS__"] != "false" {
		t.Fatalf("PROD_DEVTOOLS should stay production default, got %q", got["__VUE_PROD_DEVTOOLS__"])
	}
}

func TestMergeDefinesDevMode(t *testing.T) {
	got := mergeDefines(Options{Dev: true})
	if got["process.env.NODE_ENV"] != `"development"` {
		t.Fatalf("dev NODE_ENV = %q", got["process.env.NODE_ENV"])
	}
	if got["__VUE_PROD_DEVTOOLS__"] != "true" {
		t.Fatalf("dev PROD_DEVTOOLS = %q, want true", got["__VUE_PROD_DEVTOOLS__"])
	}
}

func TestBuildEnv(t *testing.T) {
	cmd, mode := (Options{Dev: true}).BuildEnv()
	if cmd != "serve" || mode != "development" {
		t.Fatalf("Dev BuildEnv = %s/%s", cmd, mode)
	}
	cmd, mode = (Options{Dev: true, Command: "build", Mode: "production"}).BuildEnv()
	if cmd != "build" || mode != "production" {
		t.Fatalf("override BuildEnv = %s/%s", cmd, mode)
	}
}

func TestStylesheetOutNameDistinctForSameBase(t *testing.T) {
	a := stylesheetOutName("./a/app.css", "body{color:red}", Options{})
	b := stylesheetOutName("./b/app.css", "body{color:blue}", Options{})
	if a == b {
		t.Fatalf("same basename should not collide: %q", a)
	}
	a2 := stylesheetOutName("./a/app.css", "body{color:red}", Options{Format: "esm"})
	b2 := stylesheetOutName("./b/app.css", "body{color:blue}", Options{Format: "esm"})
	if a2 == b2 {
		t.Fatalf("hashed same basename should not collide: %q vs %q", a2, b2)
	}
}
