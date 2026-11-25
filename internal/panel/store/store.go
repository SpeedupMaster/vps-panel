// Package store owns the SQLite database.
package store

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	DB     *gorm.DB // serialized writer (MaxOpenConns=1)
	ReadDB *gorm.DB // concurrent WAL read-only pool
	Path   string

	mu    sync.RWMutex
	cache map[string]string
}

// Open opens (and migrates) data/panel.db in WAL mode with separate writer and
// concurrent read-only pools.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "panel.db")
	gormCfg := &gorm.Config{Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{SlowThreshold: time.Second, LogLevel: logger.Warn, IgnoreRecordNotFoundError: true})}
	writeDSN := path + "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-8000)&_pragma=temp_store(FILE)&_pragma=journal_size_limit(8388608)"
	db, err := gorm.Open(sqlite.Open(writeDSN), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // SQLite: serialize writers, avoids SQLITE_BUSY
	if err := db.AutoMigrate(allModels()...); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	readDSN := path + "?_pragma=query_only(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=cache_size(-8000)&_pragma=temp_store(MEMORY)"
	readDB, err := gorm.Open(sqlite.Open(readDSN), gormCfg)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readConns := runtime.NumCPU()
	if readConns < 4 {
		readConns = 4
	}
	readSQLDB, _ := readDB.DB()
	readSQLDB.SetMaxOpenConns(readConns)
	readSQLDB.SetMaxIdleConns(readConns)

	st := &Store{DB: db, ReadDB: readDB, Path: path, cache: map[string]string{}}
	var all []Setting
	readDB.Find(&all)
	for _, kv := range all {
		st.cache[kv.Key] = kv.Value
	}
	return st, nil
}

// R returns the concurrent read-only WAL connection pool (falling back to DB).
func (s *Store) R() *gorm.DB {
	if s.ReadDB != nil {
		return s.ReadDB
	}
	return s.DB
}

func (s *Store) Close() error {
	var firstErr error
	if s.ReadDB != nil {
		if sqlDB, err := s.ReadDB.DB(); err == nil {
			if cerr := sqlDB.Close(); cerr != nil && firstErr == nil {
				firstErr = cerr
			}
		}
	}
	if s.DB != nil {
		if sqlDB, err := s.DB.DB(); err == nil {
			if cerr := sqlDB.Close(); cerr != nil && firstErr == nil {
				firstErr = cerr
			}
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Get returns a setting value or def.
func (s *Store) Get(key, def string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.cache[key]; ok && v != "" {
		return v
	}
	return def
}

func (s *Store) GetInt(key string, def int) int {
	v, err := strconv.Atoi(s.Get(key, ""))
	if err != nil {
		return def
	}
	return v
}

func (s *Store) GetBool(key string, def bool) bool {
	v := s.Get(key, "")
	if v == "" {
		return def
	}
	return v == "1" || v == "true"
}

func (s *Store) Set(key, value string) error {
	if err := s.DB.Save(&Setting{Key: key, Value: value}).Error; err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[key] = value
	s.mu.Unlock()
	return nil
}

// Delete removes a setting from both SQLite and the in-memory cache.
func (s *Store) Delete(key string) error {
	if err := s.DB.Delete(&Setting{}, "key = ?", key).Error; err != nil {
		return err
	}
	s.ForgetCache(key)
	return nil
}

// ForgetCache removes key from the in-memory settings cache (e.g. after a tx delete).
func (s *Store) ForgetCache(key string) {
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()
}

func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
