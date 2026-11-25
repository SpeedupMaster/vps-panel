package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"

	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/store"
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
	var daily []BackupFile
	for _, b := range p.Backups() {
		daily = append(daily, b)
	}
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

// ---------- remote targets ----------

func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

// uploadS3File PUTs a file with AWS SigV4 using streaming I/O.
func (p *Panel) uploadS3File(ctx context.Context, name, filePath string) error {
	endpoint := strings.TrimRight(p.St.Get("backup_s3_endpoint", ""), "/")
	bucket := p.St.Get("backup_s3_bucket", "")
	ak := p.St.Get("backup_s3_access_key", "")
	sk := p.SecretSetting("backup_s3_secret")
	region := p.St.Get("backup_s3_region", "auto")
	if endpoint == "" || bucket == "" || ak == "" || sk == "" {
		return errors.New("S3 配置不完整")
	}
	key := strings.Trim(p.St.Get("backup_s3_prefix", "vps-panel"), "/")
	if key != "" {
		key += "/"
	}
	key += name
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	req, err := s3SignedRequest(ctx, endpoint, region, bucket, key, ak, sk, hex.EncodeToString(h.Sum(nil)), fi.Size(), f, time.Now().UTC())
	if err != nil {
		return err
	}
	resp, err := notify.SafeHTTPClient(5 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func s3Request(ctx context.Context, endpoint, region, bucket, key, ak, sk string, body []byte, now time.Time) (*http.Request, error) {
	sum := sha256.Sum256(body)
	return s3SignedRequest(ctx, endpoint, region, bucket, key, ak, sk, hex.EncodeToString(sum[:]), int64(len(body)), bytes.NewReader(body), now)
}

func s3SignedRequest(ctx context.Context, endpoint, region, bucket, key, ak, sk, payload string, size int64, body io.Reader, now time.Time) (*http.Request, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("S3 endpoint 无效")
	}
	escKey := (&url.URL{Path: key}).EscapedPath()
	path := strings.TrimRight(u.Path, "/") + "/" + bucket + "/" + escKey
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	signed := "host;x-amz-content-sha256;x-amz-date"
	canon := "PUT\n" + path + "\n\n" + "host:" + u.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amzDate + "\n\n" + signed + "\n" + payload
	scope := day + "/" + region + "/s3/aws4_request"
	ch := sha256.Sum256([]byte(canon))
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	k := hmacSHA([]byte("AWS4"+sk), day)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, sts))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.Scheme+"://"+u.Host+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+ak+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	req.ContentLength = size
	return req, nil
}

func (p *Panel) uploadWebDAVFile(ctx context.Context, name, filePath string) error {
	base := strings.TrimRight(p.St.Get("backup_webdav_url", ""), "/")
	if base == "" {
		return errors.New("未设置 WebDAV 地址")
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("WebDAV 地址无效")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/"+name, f)
	if err != nil {
		return err
	}
	req.ContentLength = fi.Size()
	if u := p.St.Get("backup_webdav_user", ""); u != "" {
		req.SetBasicAuth(u, p.SecretSetting("backup_webdav_password"))
	}
	resp, err := notify.SafeHTTPClient(5 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *Panel) uploadTelegramFile(ctx context.Context, name, filePath string) error {
	var chs []store.NotifyChannel
	q := p.St.DB.Where("type = ?", "telegram")
	if id, _ := strconv.Atoi(p.St.Get("backup_tg_channel", "")); id > 0 {
		q = q.Where("id = ?", id)
	}
	q.Order("id").Find(&chs)
	if len(chs) == 0 {
		return errors.New("没有 Telegram 通知渠道")
	}
	cfg, err := p.ChannelConfig(&chs[0])
	if err != nil {
		return err
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if fi.Size() > 49<<20 {
		return errors.New("备份超过 Telegram 50MB 限制")
	}
	return notify.SendTelegramDocumentFile(ctx, cfg, name, filePath, "vps-panel 备份 "+time.Now().Format("2006-01-02 15:04"))
}

// ---------- restore ----------

const restoreDir = "restore-pending"

// StageRestore validates an in-memory backup and stages it on disk.
func (p *Panel) StageRestore(data []byte, pass string) error {
	plain, err := decryptBackup(data, pass)
	if err != nil {
		return err
	}
	return p.stageRestoreFromReader(bytes.NewReader(plain))
}

// StageRestoreFile validates a backup file on disk and streams its contents
// directly into restore-pending without buffering the uncompressed database in RAM.
func (p *Panel) StageRestoreFile(srcPath, pass string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	var magic [5]byte
	n, err := io.ReadFull(f, magic[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = f.Close()
		return err
	}
	if n >= len(backupMagic) && bytes.Equal(magic[:], backupMagic) {
		_ = f.Close()
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		plain, err := decryptBackup(data, pass)
		data = nil
		if err != nil {
			return err
		}
		return p.stageRestoreFromReader(bytes.NewReader(plain))
	}
	if n < 2 || magic[0] != 0x1f || magic[1] != 0x8b {
		_ = f.Close()
		return errors.New("不是有效的备份文件")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	defer f.Close()
	return p.stageRestoreFromReader(f)
}

func (p *Panel) stageRestoreFromReader(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return errors.New("备份文件已损坏")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	tmpDir := filepath.Join(p.DataDir, fmt.Sprintf(".%s-tmp-%d", restoreDir, time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	var gotDB, gotKey bool
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("备份文件已损坏")
		}
		if h.Name != "panel.db" && h.Name != "master.key" {
			continue
		}
		dst := filepath.Join(tmpDir, h.Name)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		var head [16]byte
		lr := io.LimitReader(tr, 2<<30)
		nHead, headErr := io.ReadFull(lr, head[:])
		if headErr != nil && !errors.Is(headErr, io.EOF) && !errors.Is(headErr, io.ErrUnexpectedEOF) {
			_ = out.Close()
			return headErr
		}
		if _, err := out.Write(head[:nHead]); err != nil {
			_ = out.Close()
			return err
		}
		nRest, copyErr := io.Copy(out, lr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		total := int64(nHead) + nRest
		if h.Name == "panel.db" && nHead == 16 && string(head[:]) == "SQLite format 3\x00" {
			gotDB = true
		}
		if h.Name == "master.key" && total >= 64 {
			gotKey = true
		}
	}
	if !gotDB || !gotKey {
		return errors.New("备份中缺少数据库或主密钥")
	}
	dir := filepath.Join(p.DataDir, restoreDir)
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmpDir, dir); err != nil {
		return err
	}
	ok = true
	return nil
}

// ApplyPendingRestore swaps in a staged restore before the store is opened.
// The previous database and key are kept with a .before-restore suffix.
func ApplyPendingRestore(dataDir string) error {
	dir := filepath.Join(dataDir, restoreDir)
	if _, err := os.Stat(filepath.Join(dir, "panel.db")); err != nil {
		return nil
	}
	ts := time.Now().Format("20060102-150405")
	for _, n := range []string{"panel.db", "master.key"} {
		cur := filepath.Join(dataDir, n)
		if _, err := os.Stat(cur); err == nil {
			if err := os.Rename(cur, cur+".before-restore-"+ts); err != nil {
				return err
			}
		}
		if err := os.Rename(filepath.Join(dir, n), cur); err != nil {
			return err
		}
	}
	os.Remove(filepath.Join(dataDir, "panel.db-wal"))
	os.Remove(filepath.Join(dataDir, "panel.db-shm"))
	log.Printf("restored database from backup (previous copy kept with suffix .before-restore-%s)", ts)
	return os.RemoveAll(dir)
}
