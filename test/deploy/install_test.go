package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// release makes what a release holds for this machine, and a curl that
// gives it, and a gh that says the attestation is good or not.
func release(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	osName, arch := runtime.GOOS, runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	name := "qoqi-" + osName + "-" + arch
	os.MkdirAll(filepath.Join(dir, "build", name), 0o755)
	os.WriteFile(filepath.Join(dir, "build", name, "qoqi"), []byte("#!/bin/sh\necho qoqi vtest\n"), 0o755)
	os.MkdirAll(filepath.Join(dir, "release"), 0o755)
	tarball := filepath.Join(dir, "release", name+".tar.gz")
	if out, err := exec.Command("tar", "-czf", tarball, "-C", filepath.Join(dir, "build"), name).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, out)
	}
	data, _ := os.ReadFile(tarball)
	sum := sha256.Sum256(data)
	os.WriteFile(filepath.Join(dir, "release", "checksums.txt"), []byte(hex.EncodeToString(sum[:])+"  "+name+".tar.gz\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	// What install.sh runs, and nothing else: a gh of the machine, as the
	// runners of GitHub have, is not found
	for _, tool := range []string{"sh", "cp", "tar", "gzip", "mktemp", "grep", "cut", "uname", "mkdir", "mv", "chmod", "rm", "basename", "sha256sum", "shasum"} {
		if found, err := exec.LookPath(tool); err == nil {
			os.Symlink(found, filepath.Join(dir, "bin", tool))
		}
	}
	// curl -fsSL URL -o FILE: the file of the release of that name
	os.WriteFile(filepath.Join(dir, "bin", "curl"), []byte(`#!/bin/sh
while [ $# -gt 0 ]; do
    case "$1" in
        -o) out="$2"; shift ;;
        http*) url="$1" ;;
    esac
    shift
done
cp "`+filepath.Join(dir, "release")+`/$(basename "$url")" "$out"
`), 0o755)
	return dir
}

func install(t *testing.T, dir string, gh string, env ...string) (string, error) {
	t.Helper()
	os.Remove(filepath.Join(dir, "bin", "gh"))
	if gh != "" {
		os.WriteFile(filepath.Join(dir, "bin", "gh"), []byte("#!/bin/sh\nexit "+gh+"\n"), 0o755)
	}
	cmd := exec.Command("sh", "../../install.sh")
	cmd.Env = append([]string{"PATH=" + filepath.Join(dir, "bin"), "HOME=" + dir, "QOQI_INSTALL=" + filepath.Join(dir, "installed")}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The download is checked against its attestation where gh is: one gh
// does not find attested is refused. Without gh, it is said; with
// QOQI_VERIFY=1, refused. A release from before attestations is said to
// have none.
func TestInstallChecksTheAttestation(t *testing.T) {
	dir := release(t)
	for _, c := range []struct {
		name, gh string
		env      []string
		ok       bool
		says     string
	}{
		{"attested", "0", []string{"QOQI_VERSION=v9.9.9"}, true, "Checked against its attestation"},
		{"not attested", "1", []string{"QOQI_VERSION=v9.9.9"}, false, "not attested"},
		{"without gh", "", []string{"QOQI_VERSION=v9.9.9"}, true, "install gh"},
		{"without gh, required", "", []string{"QOQI_VERSION=v9.9.9", "QOQI_VERIFY=1"}, false, "needs gh"},
		{"skipped", "1", []string{"QOQI_VERSION=v9.9.9", "QOQI_VERIFY=0"}, true, "Installed"},
		{"a release of before", "1", []string{"QOQI_VERSION=v0.2.2"}, true, "has none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			os.RemoveAll(filepath.Join(dir, "installed"))
			out, err := install(t, dir, c.gh, c.env...)
			if (err == nil) != c.ok || !strings.Contains(out, c.says) {
				t.Errorf("%v\n%s", err, out)
			}
			_, installed := os.Stat(filepath.Join(dir, "installed", "qoqi"))
			if (installed == nil) != c.ok {
				t.Errorf("installed: %v", installed == nil)
			}
		})
	}
}
