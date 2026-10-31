package orbitpi

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakeOrbitPi is a repository whose orbit/scripts/build.sh writes the built commit's label to BUILT.
func fakeOrbitPi(t *testing.T, label string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "--quiet", "-b", "main")
	commit(t, repo, label)
	return repo, git(t, repo, "rev-parse", "HEAD")
}

func commit(t *testing.T, repo, label string) string {
	t.Helper()
	os.MkdirAll(filepath.Join(repo, "orbit", "scripts"), 0o755)
	script := "#!/usr/bin/env bash\nset -e\ncd \"$(dirname \"$0\")/../..\"\necho " + label + " > BUILT\n"
	os.WriteFile(filepath.Join(repo, "orbit", "scripts", "build.sh"), []byte(script), 0o755)
	git(t, repo, "add", "orbit")
	git(t, repo, "commit", "--quiet", "-m", label)
	return git(t, repo, "rev-parse", "HEAD")
}

func TestInstall_받아서_빌드하고_상태를_남긴다(t *testing.T) {
	repo, first := fakeOrbitPi(t, "one")
	home := t.TempDir()
	var log bytes.Buffer

	st, err := Install(home, Options{Repo: repo, Ref: "origin/main", Log: &log})
	if err != nil {
		t.Fatalf("install: %v\n%s", err, log.String())
	}
	if st.Commit != first || st.Repo != repo || st.Ref != "origin/main" {
		t.Errorf("state = %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(home), "BUILT")); strings.TrimSpace(string(b)) != "one" {
		t.Errorf("빌드 결과 = %q", b)
	}
	got, err := Status(home)
	if err != nil || got.Commit != first || !got.Current {
		t.Errorf("status = %+v %v", got, err)
	}
}

func TestUpdate_새_커밋으로_다시_빌드한다(t *testing.T) {
	repo, _ := fakeOrbitPi(t, "one")
	home := t.TempDir()
	if _, err := Install(home, Options{Repo: repo, Ref: "origin/main"}); err != nil {
		t.Fatal(err)
	}
	second := commit(t, repo, "two")
	st, err := Install(home, Options{Repo: repo, Ref: "origin/main"})
	if err != nil || st.Commit != second {
		t.Fatalf("update = %+v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(home), "BUILT")); strings.TrimSpace(string(b)) != "two" {
		t.Errorf("빌드 결과 = %q", b)
	}
}

func TestInstall_빌드가_실패하면_상태를_바꾸지_않는다(t *testing.T) {
	repo, first := fakeOrbitPi(t, "one")
	home := t.TempDir()
	if _, err := Install(home, Options{Repo: repo, Ref: "origin/main"}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "orbit", "scripts", "build.sh"), []byte("#!/usr/bin/env bash\nexit 7\n"), 0o755)
	git(t, repo, "commit", "--quiet", "-am", "broken")
	if _, err := Install(home, Options{Repo: repo, Ref: "origin/main"}); err == nil {
		t.Fatal("깨진 빌드가 성공으로 끝났습니다")
	}
	got, _ := Status(home)
	if got.Commit != first || got.Current {
		t.Errorf("status = %+v (설치 기록은 그대로, 지금 checkout은 다른 커밋)", got)
	}
}

func TestStatus_설치_전에는_오류(t *testing.T) {
	if _, err := Status(t.TempDir()); err == nil {
		t.Fatal("설치 전 status가 성공했습니다")
	}
}

func TestNodeAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"v24.18.0": true, "v22.19.0": true, "v22.18.9": false, "v20.11.1": false, "v23.0.0": true, "junk": false} {
		if got := nodeAtLeast(v, 22, 19); got != want {
			t.Errorf("%s = %v", v, got)
		}
	}
}
