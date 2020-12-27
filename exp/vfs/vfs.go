package vfs

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gopub/errors"

	"github.com/gopub/types"
)

const (
	keySize         = 32
	DefaultPageSize = int64(types.MB)
	MinPageSize     = int64(32 * types.KB)
)

const (
	ErrAuth errors.String = "invalid password"
)

const (
	keyFSRootDir    = "filesystem.root"
	keyFSCredential = "filesystem.credential"
	keyFSConfig     = "filesystem.config"
	keyFSPageSize   = "filesystem.page_size"
)

type KVStorage interface {
	// Get returns os.ErrNotExist if key doesn't exist
	Get(key string) ([]byte, error)
	Put(key string, val []byte) error
	Delete(key string) error
}

func cleanName(name string) string {
	name = filepath.Clean(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, "/")
	return name
}

func splitPath(path string) []string {
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	paths := strings.Split(path, "/")
	return paths
}

func validateFileName(name string) bool {
	if name == "" {
		return false
	}

	if strings.Contains(name, "/") {
		return false
	}

	return true
}

type Flag int

const (
	ReadOnly  = Flag(os.O_RDONLY)
	WriteOnly = Flag(os.O_WRONLY)
	Create    = Flag(os.O_CREATE)
)
