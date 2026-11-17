package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vpspanel/internal/panel/store"
)

func TestBackupRoundTrip(t *testing.T) {
	p := newTestPanel(t)
	addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	if err := p.SetSecretSetting("backup_password", "s3cret!"); err != nil {
		t.Fatal(err)
	}
	if p.St.Get("backup_password", "") == "s3cret!" {
		t.Fatal("secret stored in plain text")
	}
	name, err := p.BackupNow("manual")
	if err != nil || !strings.HasSuffix(name, ".vpb") {
		t.Fatal(name, err)
	}
	path, ok := p.BackupPath(name)
	if !ok {
		t.Fatal("backup missing")
	}
	data, _ := os.ReadFile(path)
	if err := p.StageRestore(data, "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := p.StageRestore(data, "s3cret!"); err != nil {
		t.Fatal(err)
	}
	// Apply into a fresh directory, like a migration to a new server.
	dst := t.TempDir()
	os.Rename(filepath.Join(p.DataDir, restoreDir), filepath.Join(dst, restoreDir))
	if err := ApplyPendingRestore(dst); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db, _ := st.DB.DB(); db.Close() }()
	var n int64
	st.DB.Model(&store.Server{}).Where("name = ?", "HK-01").Count(&n)
	if n != 1 {
		t.Fatal("server not restored")
	}
	k1, _ := os.ReadFile(filepath.Join(p.DataDir, "master.key"))
	k2, _ := os.ReadFile(filepath.Join(dst, "master.key"))
	if string(k1) != string(k2) {
		t.Fatal("master key not restored")
	}

	// keep N
	p.St.Set("backup_keep", "2")
	for i := 0; i < 3; i++ {
		time.Sleep(1100 * time.Millisecond)
		p.BackupNow("")
	}
	if len(p.Backups()) != 2 {
		t.Fatalf("kept %d", len(p.Backups()))
	}
}

func TestAgentReleaseSignature(t *testing.T) {
	p := newTestPanel(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	spki, _ := x509.MarshalPKIXPublicKey(pub)
	p.St.Set("sign_pubkey", base64.StdEncoding.EncodeToString(spki))
	bin := append([]byte("\x7fELF"), make([]byte, 2<<20)...)
	if _, err := p.AddAgentRelease("1.2.0", "amd64", bin, "", ""); err == nil {
		t.Fatal("unsigned build accepted")
	}
	bad := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("other")))
	if _, err := p.AddAgentRelease("1.2.0", "amd64", bin, bad, ""); err == nil {
		t.Fatal("bad signature accepted")
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, bin))
	r, err := p.AddAgentRelease("v1.2.0", "amd64", bin, sig, "notes")
	if err != nil || r.Version != "1.2.0" || !strings.Contains(r.Assets, "amd64") {
		t.Fatal(r, err)
	}
	if err := p.PublishAgent("1.2.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.DataDir, "agent", "vpsagent-linux-amd64.sig")); err != nil {
		t.Fatal("published files missing")
	}
	if _, ok := p.AgentReleaseFile("1.2.0", "vpsagent-linux-amd64"); !ok {
		t.Fatal("release file not served")
	}
	if _, ok := p.AgentReleaseFile("1.2.0", "../master.key"); ok {
		t.Fatal("path traversal")
	}
	if err := p.DeleteAgentRelease("1.2.0"); err == nil {
		t.Fatal("deleted the published release")
	}
}

func TestSchedules(t *testing.T) {
	at := time.Date(2026, 11, 16, 4, 0, 0, 0, time.Local) // Monday
	cases := []struct {
		cron string
		last time.Time
		due  bool
	}{
		{"04:00", time.Time{}, true},
		{"04:00", at.Add(-time.Minute), false},
		{"04:01", time.Time{}, false},
		{"04:00@1,3", time.Time{}, true},
		{"04:00@0", time.Time{}, false},
		{"@every 6h", at.Add(-7 * time.Hour), true},
		{"@every 6h", at.Add(-time.Hour), false},
	}
	for _, c := range cases {
		if got := scheduleDue(&store.Schedule{Cron: c.cron, LastRun: c.last}, at); got != c.due {
			t.Errorf("%s last=%v: got %v", c.cron, c.last, got)
		}
	}
	for _, bad := range []string{"25:00", "@every 1m", "04:00@9", "x"} {
		if ValidateSchedule(&store.Schedule{Name: "x", Action: "unlock", Cron: bad}) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestScriptsAndS3(t *testing.T) {
	p := newTestPanel(t)
	p.SeedScripts()
	var ss []store.Script
	p.St.DB.Find(&ss)
	if len(ss) != len(BuiltinScripts) || len(ss[0].SHA256) != 64 {
		t.Fatalf("scripts %d", len(ss))
	}
	req, err := s3Request(context.Background(), "https://acc.r2.cloudflarestorage.com", "auto", "bk", "vps-panel/panel 1.vpb", "AK", "SK", []byte("x"), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://acc.r2.cloudflarestorage.com/bk/vps-panel/panel%201.vpb" ||
		!strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/20260102/auto/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Fatal(req.URL, req.Header)
	}
}
