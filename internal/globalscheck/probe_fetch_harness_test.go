package globals
import ("os";"testing")
func TestProbeFetch(t *testing.T) {
  data, err := os.ReadFile("C:/Users/issue/AppData/Local/Temp/probe_fetch.cjs")
  if err != nil { t.Fatal(err) }
  out, err := m8RunScript(t, string(data))
  if err != nil { t.Fatalf("run: %v", err) }
  t.Logf("OUTPUT:\n%s", out)
}
