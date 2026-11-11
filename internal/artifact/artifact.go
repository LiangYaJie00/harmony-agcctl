// Package artifact guards the one read-only APP Pack bound to a PUBLISH task.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
)

// Resolve verifies that a supplied APP Pack is an absolute regular file inside
// artifactRoot/<taskID>. Symlinks escaping the task directory are rejected.
func Resolve(artifactRoot, taskID, supplied string) (string, error) {
	if !filepath.IsAbs(supplied) || hasParentSegment(supplied) {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_PATH_REJECTED", "APP Pack 路径必须是任务产物目录内的绝对路径")
	}
	root, err := realPath(artifactRoot)
	if err != nil {
		root, err = filepath.Abs(artifactRoot)
		if err != nil {
			return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_PATH_REJECTED", "任务目录不在产物根目录内")
		}
	}
	root = filepath.Clean(root)
	taskRoot := filepath.Join(root, taskID)
	if resolved, resolveErr := filepath.EvalSymlinks(taskRoot); resolveErr == nil {
		taskRoot = resolved
	} else {
		taskRoot = filepath.Clean(taskRoot)
	}
	if !inside(taskRoot, root) {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_PATH_REJECTED", "任务目录不在产物根目录内")
	}

	info, err := os.Lstat(supplied)
	if err != nil {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_MISSING", "APP Pack 文件不存在")
	}
	real, err := filepath.EvalSymlinks(supplied)
	if err != nil {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_MISSING", "APP Pack 文件不存在")
	}
	if info.Mode()&os.ModeSymlink != 0 && !inside(real, taskRoot) {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_SYMLINK_REJECTED", "APP Pack 软链接指向任务目录之外")
	}
	if !inside(real, taskRoot) {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_PATH_REJECTED", "APP Pack 不在当前任务产物目录内")
	}
	stat, err := os.Stat(real)
	if err != nil || !stat.Mode().IsRegular() {
		return "", apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_MISSING", "APP Pack 文件不存在")
	}
	return real, nil
}

// SizeAndSHA256 streams the file to avoid loading an APP Pack into memory.
func SizeAndSHA256(path string) (int64, string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer handle.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, handle)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

func hasParentSegment(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func realPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func inside(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
