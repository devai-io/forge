package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIsSecretCandidate(t *testing.T) {
	yes := []string{".env", "api/.env.production", "prod.env", "certs/tls.key", "certs/tls.pem", "AuthKey_X.p8", "android/app/upload.jks",
		"android/key.properties", "android/app/google-services.json", "ios/Runner/GoogleService-Info.plist",
		"service-account.json", "credentials-prod.json", "infra/prod.tfvars", "config/secrets.yml", "dist.mobileprovision"}
	no := []string{".env.example", ".env.sample", ".env.template", "android/local.properties", "node_modules/x/.env",
		"vendor/pkg/key.pem", "build/out.jks", "README.md", "package.json", "src/app.ts", "config/settings.json"}
	for _, p := range yes {
		if !IsSecretCandidate(p) {
			t.Errorf("%s should be a candidate", p)
		}
	}
	for _, p := range no {
		if IsSecretCandidate(p) {
			t.Errorf("%s should not be a candidate", p)
		}
	}
}

func TestScanSecrets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	write := func(p, s string) {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".env\n*.jks\nnode_modules/\nempty.pem\n")
	write(".env", "A=1\n")
	write(".env.example", "A=\n")         // tracked-able, not ignored
	write("android/upload.jks", "binary") // ignored
	write("node_modules/x/.env", "B=2\n") // ignored, but skipped
	write("empty.pem", "")                // ignored, but empty
	write("notignored.pem", "k")          // not ignored: in git's hands
	files, err := scanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".env", "android/upload.jks"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("scan = %v, want %v", files, want)
	}
}

func TestBatchFiles(t *testing.T) {
	files := []pushFile{{Path: "a", ContentBase64: strings.Repeat("x", 6)}, {Path: "b", ContentBase64: strings.Repeat("x", 6)}, {Path: "c", ContentBase64: "x"}}
	if got := batchFiles(files, 10); len(got) != 2 || len(got[0]) != 1 || len(got[1]) != 2 {
		t.Fatalf("batches = %v", got)
	}
}
