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

	zr, cleanup, err := download(context.Background(), Source{Version: "26.11.9", BaseURL: srv.URL, AllowedPanelURLs: []string{srv.URL}}, func(string) {})
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

func TestSourceValidate(t *testing.T) {
	panels := []string{"https://panel.example.com", "http://10.0.0.2:8080/sub"}
	good := []Source{
		{Version: "26.1.1"},
		{Version: "26.1.1", BaseURL: "https://github.com/XTLS/Xray-core/releases/download/v26.1.1"},
		{Version: "26.1.1", BaseURL: "https://panel.example.com/dl/xray/v26.1.1/", AllowedPanelURLs: panels},
		{Version: "26.1.1", BaseURL: "https://panel.example.com:443/dl/xray/v26.1.1/", AllowedPanelURLs: panels},
		{Version: "26.1.1", BaseURL: "http://10.0.0.2:8080/sub/dl/xray/v26.1.1", AllowedPanelURLs: panels},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Source%+v unexpectedly rejected: %v", s, err)
		}
	}
	bad := []Source{
		{Version: "../evil"},
		{Version: "26.1.1", BaseURL: "http://github.com/XTLS/Xray-core/releases/download/v26.1.1"},
		{Version: "26.1.1", BaseURL: "https://github.com/evil/Xray-core/releases/download/v26.1.1"},
		{Version: "26.1.1", BaseURL: "https://evil.example.com/dl/xray/v26.1.1", AllowedPanelURLs: panels},
		{Version: "26.1.1", BaseURL: "https://panel.example.com/../evil", AllowedPanelURLs: panels},
		{Version: "26.1.1", BaseURL: "http://10.0.0.2:8080/other/xray", AllowedPanelURLs: panels},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("Source%+v unexpectedly allowed", s)
		}
	}
}

func TestApplyFailClosedOnFirewallError(t *testing.T) {
	dir := t.TempDir()
	oldBin, oldConf, oldFW := Bin, ConfDir, firewallApply
	defer func() { Bin, ConfDir, firewallApply = oldBin, oldConf, oldFW }()

	Bin = filepath.Join(dir, "xray")
	ConfDir = filepath.Join(dir, "etc")
	if err := os.WriteFile(Bin, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	firewallApply = func(port int, allowed []string) error {
		return errors.New("nft failed")
	}

	m := &Manager{dataDir: dir}
	err := m.Apply(context.Background(), &proto.ApplyConfig{
		Version:  2,
		Config:   []byte(`{"inbounds":[]}`),
		Firewall: &proto.FirewallPolicy{Port: 8388, Allowed: []string{"1.2.3.4"}},
	}, func(string) {})
	if err == nil {
		t.Fatal("expected Apply to fail closed when firewallApply returns error")
	}
	if _, statErr := os.Stat(confPath()); !os.IsNotExist(statErr) {
		t.Fatalf("config.json should not be written on firewall failure, stat err=%v", statErr)
	}
}

func TestOpRemoveUserRunsRmuBeforeRmrulesAndWritesAtomic(t *testing.T) {
	dir := t.TempDir()
	oldConf, oldActive, oldRun := ConfDir, serviceActive, runXrayAPI
	defer func() { ConfDir, serviceActive, runXrayAPI = oldConf, oldActive, oldRun }()

	ConfDir = filepath.Join(dir, "etc")
	serviceActive = func() bool { return true }

	var calls []string
	runXrayAPI = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) >= 2 {
			calls = append(calls, args[1])
		}
		return []byte("ok"), nil
	}

	m := &Manager{dataDir: dir}
	cfg := []byte(`{"inbounds":[],"outbounds":[]}`)
	err := m.Op(context.Background(), &proto.XrayOp{
		Op:         "remove_user",
		Tag:        "reality-in",
		User:       &proto.XrayUser{Email: "r-1"},
		Rule:       []byte(`{"ruleTag":"r-1"}`),
		FullConfig: cfg,
		Version:    7,
	}, func(string) {})
	if err != nil {
		t.Fatalf("Op remove_user failed: %v", err)
	}
	if len(calls) != 2 || calls[0] != "rmu" || calls[1] != "rmrules" {
		t.Fatalf("expected [rmu rmrules], got %v", calls)
	}
	written, err := os.ReadFile(confPath())
	if err != nil {
		t.Fatalf("config.json missing: %v", err)
	}
	if !bytes.Equal(written, pretty(cfg)) {
		t.Fatalf("config.json content mismatch:\n%s", written)
	}
	// Ensure no stray temp files remain in ConfDir.
	entries, err := os.ReadDir(ConfDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("unexpected files in ConfDir: %v", entries)
	}
}

func TestOpFallbackToApplyLockedDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	oldBin, oldConf, oldActive := Bin, ConfDir, serviceActive
	defer func() { Bin, ConfDir, serviceActive = oldBin, oldConf, oldActive }()

	Bin = filepath.Join(dir, "missing-xray")
	ConfDir = filepath.Join(dir, "etc")
	serviceActive = func() bool { return false }

	m := &Manager{dataDir: dir}
	err := m.Op(context.Background(), &proto.XrayOp{
		Op:         "add_user",
		Tag:        "reality-in",
		User:       &proto.XrayUser{Email: "r-2"},
		FullConfig: []byte(`{"inbounds":[]}`),
		Version:    3,
	}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Xray 未安装") {
		t.Fatalf("expected fallback applyLocked error without deadlock, got %v", err)
	}
}

func TestUpdateGeoFailClosedAndCleansBak(t *testing.T) {
	dir := t.TempDir()
	oldAsset, oldConf, oldBin, oldActive, oldTest := AssetDir, ConfDir, Bin, serviceActive, testXrayConfig
	oldSrc := GeoSources["test"]
	defer func() {
		AssetDir, ConfDir, Bin, serviceActive, testXrayConfig = oldAsset, oldConf, oldBin, oldActive, oldTest
		if oldSrc == [2]string{} {
			delete(GeoSources, "test")
		} else {
			GeoSources["test"] = oldSrc
		}
	}()

	AssetDir = filepath.Join(dir, "share")
	ConfDir = filepath.Join(dir, "etc")
	Bin = filepath.Join(dir, "xray")
	serviceActive = func() bool { return false }
	testXrayConfig = func(ctx context.Context, bin, path string) (string, error) { return "ok", nil }

	if err := os.MkdirAll(AssetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origGeoIP := []byte("old-geoip")
	origGeoSite := []byte("old-geosite")
	_ = os.WriteFile(filepath.Join(AssetDir, "geoip.dat"), origGeoIP, 0o644)
	_ = os.WriteFile(filepath.Join(AssetDir, "geosite.dat"), origGeoSite, 0o644)

	newGeoIP := []byte("new-geoip-data")
	newGeoSite := []byte("new-geosite-data")
	sumIP := sha256.Sum256(newGeoIP)
	sumSite := sha256.Sum256(newGeoSite)

	failChecksum := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/geoip.dat":
			_, _ = w.Write(newGeoIP)
		case "/geoip.dat.sha256sum":
			if failChecksum {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(hex.EncodeToString(sumIP[:]) + "  geoip.dat\n"))
		case "/geosite.dat":
			_, _ = w.Write(newGeoSite)
		case "/geosite.dat.sha256sum":
			_, _ = w.Write([]byte(hex.EncodeToString(sumSite[:]) + "  geosite.dat\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	GeoSources["test"] = [2]string{srv.URL + "/geoip.dat", srv.URL + "/geosite.dat"}
	m := &Manager{dataDir: dir}

	// 1) Missing .sha256sum must fail closed and keep original files intact.
	if err := m.UpdateGeo(context.Background(), "test", func(string) {}); err == nil {
		t.Fatal("expected UpdateGeo to fail closed when .sha256sum download fails")
	}
	if got, _ := os.ReadFile(filepath.Join(AssetDir, "geoip.dat")); !bytes.Equal(got, origGeoIP) {
		t.Fatalf("geoip.dat modified on failed checksum: %q", got)
	}

	// 2) Valid .sha256sum updates both files and removes .bak backups.
	failChecksum = false
	if err := m.UpdateGeo(context.Background(), "test", func(string) {}); err != nil {
		t.Fatalf("UpdateGeo failed with valid checksums: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(AssetDir, "geoip.dat")); !bytes.Equal(got, newGeoIP) {
		t.Fatalf("geoip.dat = %q, want %q", got, newGeoIP)
	}
	if got, _ := os.ReadFile(filepath.Join(AssetDir, "geosite.dat")); !bytes.Equal(got, newGeoSite) {
		t.Fatalf("geosite.dat = %q, want %q", got, newGeoSite)
	}
	for _, name := range []string{"geoip.dat.bak", "geosite.dat.bak"} {
		if _, err := os.Stat(filepath.Join(AssetDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed after verification", name)
		}
	}
}

func TestSystemdUnitSandboxing(t *testing.T) {
	for _, want := range []string{
		"NoNewPrivileges=true",
		"ProtectSystem=full",
		"ProtectHome=true",
		"PrivateTmp=true",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_DAC_OVERRIDE",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW",
	} {
		if !strings.Contains(unitFile, want) {
			t.Errorf("xray.service unitFile missing %q", want)
		}
	}
}
