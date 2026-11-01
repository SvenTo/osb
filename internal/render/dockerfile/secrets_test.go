package dockerfile

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSubstituteSecrets(t *testing.T) {
	secrets := []Secret{{ID: "password", Env: "OSB_ARG_PASSWORD", Value: "s3cr3t pw"}, {ID: "token", Env: "OSB_ARG_TOKEN", Value: "tok-123"}}
	script := "printf '%s:%s\\n' alice 's3cr3t pw' | chpasswd\n" +
		"echo \"token: tok-123\"\n" +
		"curl -H tok-123 https://example.com # tok-123 in a comment\n" +
		"write_file /etc/a 0600 <<'OSB_EOF'\n" +
		"secret=s3cr3t pw\n" +
		"price=$5 and `cmd` \\n\n" +
		"OSB_EOF\n" +
		"cat <<END\n" +
		"x tok-123\n" +
		"END\n"
	out, used, err := substituteSecrets(script, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 2 {
		t.Errorf("used = %v", used)
	}
	for _, s := range secrets {
		if strings.Contains(out, s.Value) {
			t.Errorf("secret %s still in output:\n%s", s.ID, out)
		}
	}
	// The result must behave exactly like the original when the variables are set.
	run := func(s string, env ...string) string {
		cmd := exec.Command("sh", "-c", `write_file() { cat; }; chpasswd() { cat; }; curl() { echo curl "$@"; }; `+s)
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s\n%s", err, b, s)
		}
		return string(b)
	}
	want := run(script)
	got := run(out, "OSB_ARG_PASSWORD=s3cr3t pw", "OSB_ARG_TOKEN=tok-123")
	if want != got {
		t.Errorf("output differs:\nwant %q\n got %q\nscript:\n%s", want, got, out)
	}
}
