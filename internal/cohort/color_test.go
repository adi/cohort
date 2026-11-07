package cohort

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestCksumMatchesCoreutils(t *testing.T) {
	if _, err := exec.LookPath("cksum"); err != nil {
		t.Skip("cksum not installed")
	}
	for _, s := range []string{"", "a", "api", "cohort-manager", "cloudflare", strings.Repeat("x", 300)} {
		cmd := exec.Command("cksum")
		cmd.Stdin = strings.NewReader(s)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		want, _ := strconv.ParseUint(strings.Fields(string(out))[0], 10, 32)
		if got := cksum([]byte(s)); uint64(got) != want {
			t.Errorf("cksum(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestColorOptions(t *testing.T) {
	o := colorOptions("api")
	if !strings.HasPrefix(o["@cohort_color"], "colour") || len(strings.Fields(o["@cohort_rgb"])) != 3 {
		t.Fatalf("unexpected: %v", o)
	}
	if r, g, b := xtermRGB(16); r+g+b != 0 {
		t.Error("colour16 is black")
	}
	if r, g, b := xtermRGB(231); r != 255 || g != 255 || b != 255 {
		t.Error("colour231 is white")
	}
	if r, _, _ := xtermRGB(232); r != 8 {
		t.Error("colour232 is grey 8")
	}
}
