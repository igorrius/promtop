package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestFetchScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("echo hi\n"))
		case "/cut":
			// Promises more than it sends, as a connection lost midway does.
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("echo hi\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := fetchScript(context.Background(), srv.URL+"/ok")
	if err != nil || string(got) != "echo hi\n" {
		t.Errorf("ok: got %q, %v", got, err)
	}
	if _, err := fetchScript(context.Background(), srv.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing: want a 404 error, got %v", err)
	}
	if _, err := fetchScript(context.Background(), srv.URL+"/cut"); err == nil {
		t.Error("cut off download: want an error")
	}
}

func TestUpdateEnvOverridesInstallDir(t *testing.T) {
	got := updateEnv([]string{"A=1", "PROMTOP_INSTALL_DIR=/elsewhere", "PROMTOP_VERSION=v1.0.0"}, "/here")
	want := []string{"A=1", "PROMTOP_VERSION=v1.0.0", "PROMTOP_INSTALL_DIR=/here"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPackageManaged(t *testing.T) {
	for dir, want := range map[string]bool{
		"/usr/bin":        true,
		"/bin":            true,
		"/usr/local/bin":  false,
		"/home/me/go/bin": false,
	} {
		if got := packageManaged(dir); got != want {
			t.Errorf("packageManaged(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestUpdateArguments(t *testing.T) {
	if code := runUpdate([]string{"-h"}); code != 0 {
		t.Errorf("-h: exit %d, want 0", code)
	}
	if code := runUpdate([]string{"extra"}); code != 2 {
		t.Errorf("an argument: exit %d, want 2", code)
	}
	if code := runUpdate([]string{"-nope"}); code != 2 {
		t.Errorf("an unknown flag: exit %d, want 2", code)
	}
}
