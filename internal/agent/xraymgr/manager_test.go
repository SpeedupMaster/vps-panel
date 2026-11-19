package xraymgr

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vpspanel/internal/common/proto"
)

func TestParseStats(t *testing.T) {
	out := []byte(`{"stat":[
	{"name":"user>>>r-3>>>traffic>>>uplink","value":"100"},
	{"name":"user>>>r-3>>>traffic>>>downlink","value":250},
	{"name":"inbound>>>api>>>traffic>>>uplink","value":"9"},
	{"name":"outbound>>>direct>>>traffic>>>downlink","value":"7"}]}`)
	items := parseStats(out)
	if len(items) != 2 || items[0].Name != "r-3" || items[0].Up != 100 || items[0].Down != 250 || items[1].Scope != "outbound" {
		t.Fatalf("%+v", items)
	}
}

func TestInboundWithUser(t *testing.T) {
	full := []byte(`{"inbounds":[{"tag":"reality-in","port":443,"protocol":"vless","settings":{"clients":[{"id":"a"}],"decryption":"none"}}]}`)
	b, err := inboundWithUser(full, "reality-in", &proto.XrayUser{Email: "r-9", ID: "u", Flow: "xtls-rprx-vision"})
	if err != nil || !strings.Contains(string(b), `"email":"r-9"`) || strings.Contains(string(b), `"id":"a"`) {
		t.Fatalf("%s %v", b, err)
	}
}
func TestAduAdded(t *testing.T) {
	cases := map[string]bool{
		"result: ok\nAdded 1 user(s) in total.\n": true,
		"Added 0 user(s) in total.\n":             false,
		"failed to add user: ...":                 false,
		"":                                        true, // older Xray prints nothing on success
	}
	for out, want := range cases {
		if got := aduAdded(out); got != want {
			t.Errorf("aduAdded(%q) = %v, want %v", out, got, want)
		}
	}
}

func TestInstalledRequiresServiceFile(t *testing.T) {
	dir := t.TempDir()
	oldBin, oldSys, oldRC := Bin, systemdUnitPath, openrcInitPath
	defer func() { Bin, systemdUnitPath, openrcInitPath = oldBin, oldSys, oldRC }()

	Bin = filepath.Join(dir, "xray")
	systemdUnitPath = filepath.Join(dir, "xray.service")
	openrcInitPath = filepath.Join(dir, "xray.init")

	if err := os.WriteFile(Bin, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Installed() {
		t.Fatal("Installed() should be false when service file is missing")
	}
	svcPath := openrcInitPath
	if systemd() {
		svcPath = systemdUnitPath
	}
	if err := os.WriteFile(svcPath, []byte("unit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Installed() {
		t.Fatal("Installed() should be true when both binary and service file exist")
	}
}

func TestDownloadStreamsToDiskAndVerifiesDigest(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("xray")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("fake-xray-binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := buf.Bytes()
	sum := sha256.Sum256(zipBytes)
	dgst := "SHA2-256= " + hex.EncodeToString(sum[:]) + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".dgst"):
			_, _ = w.Write([]byte(dgst))
		default:
			_, _ = w.Write(zipBytes)
		}
	}))
	defer srv.Close()

	zr, cleanup, err := download(context.Background(), Source{Version: "26.11.9", BaseURL: srv.URL}, func(string) {})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer cleanup()

	dst := filepath.Join(t.TempDir(), "xray")
	if err := extract(&zr.Reader, "xray", dst, 0o755); err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "fake-xray-binary" {
		t.Fatalf("extracted = %q, err = %v", got, err)
	}
	cleanup()
}

func TestFetchToFileEnforcesMaxSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "too-big")
	if _, _, err := fetchToFile(context.Background(), srv.URL, dst, 5, 0o600); err == nil {
		t.Fatal("expected size limit error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to be removed on size error, stat err=%v", err)
	}
}

func TestStartErrDetailIncludesRestartErr(t *testing.T) {
	oldLog := openrcLogPath
	defer func() { openrcLogPath = oldLog }()
	openrcLogPath = filepath.Join(t.TempDir(), "missing.log")

	msg := startErrDetail(context.Background(), errors.New("systemctl restart xray: Unit xray.service not found."))
	if !strings.Contains(msg, "Unit xray.service not found.") {
		t.Fatalf("expected restart error in detail, got %q", msg)
	}
}
