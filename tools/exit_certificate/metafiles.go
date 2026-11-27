package exit_certificate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agglayer/aggkit/log"
)

// dirMetaFiles is the subdirectory of options.outputDir where --step upload copies options.archiveMetaFiles.
const dirMetaFiles = "metafiles"

// validateArchiveMetaFiles checks options.archiveMetaFiles: no empty entry and unique basenames (every file
// is copied as metafiles/<basename>, so two files with the same name would overwrite each other).
func validateArchiveMetaFiles(files []string) error {
	seen := make(map[string]string, len(files))
	for i, f := range files {
		if strings.TrimSpace(f) == "" {
			return fmt.Errorf("invalid options.archiveMetaFiles[%d]: empty path", i)
		}
		base := filepath.Base(filepath.Clean(f))
		if prev, ok := seen[base]; ok {
			return fmt.Errorf("invalid options.archiveMetaFiles: %q and %q have the same file name %q "+
				"(both would be copied to %s/%s)", prev, f, base, dirMetaFiles, base)
		}
		seen[base] = f
	}
	return nil
}

// copyMetaFiles copies every options.archiveMetaFiles entry, as is, into <outputDir>/metafiles/<basename>
// (files there that are no longer listed are removed). runUpload calls it right before collecting the files
// to archive, so the meta files are uploaded, zstd-compressed once, like any other file of the run.
func copyMetaFiles(opts Options) error {
	if len(opts.ArchiveMetaFiles) == 0 {
		return nil
	}
	dir := filepath.Join(opts.OutputDir, dirMetaFiles)
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	keep := make(map[string]bool, len(opts.ArchiveMetaFiles))
	for _, src := range opts.ArchiveMetaFiles {
		name := filepath.Base(filepath.Clean(src))
		dst := filepath.Join(dir, name)
		if err := copyMetaFile(src, dst); err != nil {
			return fmt.Errorf("options.archiveMetaFiles: %w", err)
		}
		keep[name] = true
		log.Infof("Meta file %s copied to %s", src, dst)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		p := filepath.Join(dir, e.Name())
		log.Infof("Removing %s: not in options.archiveMetaFiles", p)
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return nil
}

// copyMetaFile copies src to dst (through dst+".partial", renamed once complete).
func copyMetaFile(src, dst string) (err error) {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("meta file %s: %w", src, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("meta file %s is not a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open meta file %s: %w", src, err)
	}
	defer in.Close()

	tmp := dst + ".partial"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePermissions)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, dst, err)
	}
	return nil
}
