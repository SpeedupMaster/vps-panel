package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

// ---------- secret settings ----------

// SecretSettings are stored encrypted and never returned to the UI.
var SecretSettings = []string{"backup_password", "backup_s3_secret", "backup_webdav_password", "ipapi_is_key"}

const SecretMask = "••••••"

func (p *Panel) SecretSetting(key string) string {
	enc := p.St.Get(key, "")
	if enc == "" {
		return ""
	}
	s, err := p.Box.Open(enc)
	if err != nil {
		return ""
	}
	return s
}

// SetSecretSetting stores a secret; the mask value keeps the current one.
func (p *Panel) SetSecretSetting(key, value string) error {
	if value == SecretMask {
		return nil
	}
	if value == "" {
		return p.St.Set(key, "")
	}
	return p.St.Set(key, p.Box.Seal(value))
}

// ---------- archive format ----------

var backupMagic = []byte("VPSB1")

func (p *Panel) backupDir() string { return filepath.Join(p.DataDir, "backups") }

// BackupFile describes a local backup.
type BackupFile struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Encrypted bool      `json:"encrypted"`
	At        time.Time `json:"at"`
}

func (p *Panel) Backups() []BackupFile {
	es, _ := os.ReadDir(p.backupDir())
	var out []BackupFile
	for _, e := range es {
		if e.IsDir() || !backupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{Name: e.Name(), Size: info.Size(), Encrypted: strings.HasSuffix(e.Name(), ".vpb"), At: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

var backupName = regexp.MustCompile(`^panel-\d{8}-\d{6}(-[a-z-]+)?\.(vpb|tar\.gz)$`)

func (p *Panel) BackupPath(name string) (string, bool) {
	if !backupName.MatchString(name) {
		return "", false
	}
	path := filepath.Join(p.backupDir(), name)
	_, err := os.Stat(path)
	return path, err == nil
}

func (p *Panel) DeleteBackup(name string) error {
	path, ok := p.BackupPath(name)
	if !ok {
		return errors.New("备份不存在")
	}
	return os.Remove(path)
}

// snapshotToFile writes a tar.gz containing a consistent copy of panel.db and
// master.key directly to dst using streaming I/O (no full-DB RAM buffering).
func (p *Panel) snapshotToFile(dst string) error {
	tmp := filepath.Join(p.DataDir, fmt.Sprintf(".snapshot-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := p.St.DB.Exec("VACUUM INTO ?", tmp).Error; err != nil {
		return fmt.Errorf("数据库快照失败：%w", err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	add := func(name, path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	}
	if err := add("panel.db", tmp); err != nil {
		return fail(err)
	}
	if err := add("master.key", filepath.Join(p.DataDir, "master.key")); err != nil {
		return fail(err)
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func encryptBackupFile(src, dst, pass string) error {
	plain, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	enc, err := encryptBackup(plain, pass)
	plain = nil
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func encryptBackup(plain []byte, pass string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(pass), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append(append([]byte{}, backupMagic...), salt...), nonce...)
	return gcm.Seal(out, nonce, plain, backupMagic), nil
}

func decryptBackup(data []byte, pass string) ([]byte, error) {
	if !bytes.HasPrefix(data, backupMagic) {
		if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
			return data, nil // plain tar.gz
		}
		return nil, errors.New("不是有效的备份文件")
	}
	if pass == "" {
		return nil, errors.New("该备份已加密，请输入备份密码")
	}
	data = data[len(backupMagic):]
	if len(data) < 16+12+16 {
		return nil, errors.New("备份文件已损坏")
	}
	salt, nonce, ct := data[:16], data[16:28], data[28:]
	key, err := scrypt.Key([]byte(pass), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, backupMagic)
	if err != nil {
		return nil, errors.New("备份密码错误或文件已损坏")
	}
	return plain, nil
}

// BackupNow writes a backup, prunes old ones and uploads to remote targets.
func (p *Panel) BackupNow(reason string) (string, error) {
	p.backupMu.Lock()
	defer p.backupMu.Unlock()

	if err := os.MkdirAll(p.backupDir(), 0o700); err != nil {
		return "", err
	}
	tmpTar := filepath.Join(p.backupDir(), fmt.Sprintf(".tmp-%d.tar.gz", time.Now().UnixNano()))
	defer os.Remove(tmpTar)
	if err := p.snapshotToFile(tmpTar); err != nil {
		return "", err
	}
	pass := p.SecretSetting("backup_password")
	ext := "tar.gz"
	if pass != "" {
		ext = "vpb"
	}
	name := "panel-" + time.Now().Format("20060102-150405")
	if reason != "" && reason != "daily" {
		name += "-" + reason
	}
	name += "." + ext
	finalPath := filepath.Join(p.backupDir(), name)
	if pass != "" {
		if err := encryptBackupFile(tmpTar, finalPath, pass); err != nil {
			return "", err
		}
		_ = os.Remove(tmpTar)
	} else {
		if err := os.Rename(tmpTar, finalPath); err != nil {
			return "", err
		}
	}
	p.pruneBackups()

	var errs []string
	if pass != "" { // never ship unencrypted backups off the machine
		for _, t := range strings.Split(p.St.Get("backup_target", ""), ",") {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			var err error
			switch strings.TrimSpace(t) {
			case "s3":
				err = p.uploadS3File(ctx, name, finalPath)
			case "webdav":
				err = p.uploadWebDAVFile(ctx, name, finalPath)
			case "telegram":
				err = p.uploadTelegramFile(ctx, name, finalPath)
			}
			cancel()
			if err != nil {
				errs = append(errs, t+": "+err.Error())
			}
		}
	} else if strings.TrimSpace(p.St.Get("backup_target", "")) != "" {
		errs = append(errs, "未设置备份密码，已跳过远程上传")
	}
	_ = p.St.Set("backup_last", time.Now().Format(time.RFC3339)+" "+name)
	if len(errs) > 0 {
		msg := "备份 " + name + " 已保存到本地，远程上传失败：" + strings.Join(errs, "；")
		_ = p.St.Set("backup_last_error", msg)
		p.Alert.Info("backup", 0, msg)
		return name, nil
	}
	_ = p.St.Set("backup_last_error", "")
	return name, nil
}

func (p *Panel) pruneBackups() {
	keep := p.St.GetInt("backup_keep", 7)
	if keep < 1 {
		keep = 1
	}
	daily := p.Backups()
	for i := keep; i < len(daily); i++ {
		os.Remove(filepath.Join(p.backupDir(), daily[i].Name))
	}
}

// DailyBackup is the scheduler hook.
func (p *Panel) DailyBackup() {
	if !p.St.GetBool("backup_enabled", true) {
		return
	}
	if _, err := p.BackupNow("daily"); err != nil {
		log.Printf("backup: %v", err)
		p.Alert.Info("backup", 0, "每日备份失败："+err.Error())
	}
}
