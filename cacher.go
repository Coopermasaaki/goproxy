package goproxy

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

// Cacher defines a set of intuitive methods used to cache module files for the
// [Goproxy].
type Cacher interface {
	// Get gets the matched cache for the name. It returns the
	// [os.ErrNotExist] if not found.
	//
	// Note that the returned [io.ReadCloser] can optionally implement the
	// following interfaces:
	//  1. [io.Seeker], mostly for the Range request header.
	//  2. interface{ LastModified() time.Time }, mostly for the
	//     Last-Modified response header. Also for the If-Unmodified-Since,
	//     If-Modified-Since and If-Range request headers when 1 is
	//     implemented.
	//  3. interface{ ModTime() time.Time }, same as 2, but with lower
	//     priority.
	//  4. interface{ ETag() string }, mostly for the ETag response header.
	//     Also for the If-Match, If-None-Match and If-Range request headers
	//     when 1 is implemented. Note that the return value will be assumed
	//     to have complied with RFC 7232, section 2.3, so it will be used
	//     directly without further processing.
	Get(ctx context.Context, name string) (io.ReadCloser, error)

	// Put puts a cache for the name with the content and sets it to expire after the given duration.
	Put(ctx context.Context, name string, content io.ReadSeeker, expiration time.Duration) error

	// Cleanup removes all expired cache files.
	Cleanup() error
}

// DirCacher implements the [Cacher] using a directory on the local disk. If the
// directory does not exist, it will be created with 0750 permissions.
type DirCacher string

// Get implements the [Cacher].
func (dc DirCacher) Get(
	ctx context.Context,
	name string,
) (io.ReadCloser, error) {
	filePath := filepath.Join(string(dc), filepath.FromSlash(name))

	// Check if the file has expired
	expired, err := isCacheExpired(filePath)
	if err != nil {
		return nil, err
	}
	if expired {
		return nil, os.ErrNotExist
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	return &struct {
		*os.File
		os.FileInfo
	}{f, fi}, nil
}

// Put implements the [Cacher].
func (dc DirCacher) Put(
	ctx context.Context,
	name string,
	content io.ReadSeeker,
	expiration time.Duration,
) error {
	file := filepath.Join(string(dc), filepath.FromSlash(name))

	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	f, err := ioutil.TempFile(dir, fmt.Sprintf(
		".%s.tmp*",
		filepath.Base(file),
	))
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	if _, err := io.Copy(f, content); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(f.Name(), file); err != nil {
		return err
	}

	// Set the expiration time
	if err := setCacheExpiration(file, expiration); err != nil {
		return err
	}

	return nil
}

// Cleanup implements the [Cacher].
func (dc DirCacher) Cleanup() error {
	files, err := ioutil.ReadDir(string(dc))
	if err != nil {
		return err
	}

	for _, file := range files {
		filePath := filepath.Join(string(dc), file.Name())
		expired, err := isCacheExpired(filePath)
		if err != nil {
			return err
		}
		if expired {
			if err := os.Remove(filePath); err != nil {
				return err
			}
		}
	}

	return nil
}

// isCacheExpired checks if the cache file at the specified path has expired.
func isCacheExpired(filePath string) (bool, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}

	expirationTime := info.ModTime().Add(24 * time.Hour)
	return time.Now().After(expirationTime), nil
}

// setCacheExpiration sets the expiration time for the cache file at the specified path.
func setCacheExpiration(filePath string, expiration time.Duration) error {
	expirationTime := time.Now().Add(expiration)
	return os.Chtimes(filePath, time.Now(), expirationTime)
}

// StartCleanupTask starts a periodic cleanup task for the cache directory.
// It cleans up expired cache files every duration interval.
func StartCleanupTask(dirCacher DirCacher, interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			if err := dirCacher.Cleanup(); err != nil {
				fmt.Printf("Error cleaning up expired cache files: %v\n", err)
			}
		}
	}()
}
