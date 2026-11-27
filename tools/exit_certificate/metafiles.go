package exit_certificate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/klauspost/compress/zstd"
)

const (
	// dirMetaFiles is the subdirectory of options.outputDir holding the options.archiveMetaFiles copies.
	dirMetaFiles = "metafiles"
	// metaFileExt is appended to the basename of every copied meta file (they are stored zstd-compressed).
	metaFileExt = ".zst"
)

// validateArchiveMetaFiles checks options.archiveMetaFiles: no empty entry and unique basenames (every file
// is copied as metafiles/<basename>.zst, so two files with the same name would overwrite each other).
func validateArchiveMetaFiles(files []string) error {
	seen := make(map[string]string, len(files))
	for i, f := range files {
		if strings.TrimSpace(f) == "" {
			return fmt.Errorf("invalid options.archiveMetaFiles[%d]: empty path", i)
		}
		base := filepath.Base(filepath.Clean(f))
		if prev, ok := seen[base]; ok {
			return fmt.Errorf("invalid options.archiveMetaFiles: %q and %q have the same file name %q "+
				"(both would be copied to %s/%s%s)", prev, f, base, dirMetaFiles, base, metaFileExt)
		}
		seen[base] = f
	}
	return nil
}

// metaFileName is the path, relative to options.outputDir, of the copy of the meta file src.
func metaFileName(src string) string {
	return filepath.Join(dirMetaFiles, filepath.Base(filepath.Clean(src))+metaFileExt)
}

// copyMetaFiles copies every options.archiveMetaFiles entry, zstd-compressed, into
// <outputDir>/metafiles/<basename>.zst (files there that are no longer listed are removed), so they are part
// of the run's result and --step upload archives them with the rest of the output dir.
// When the output dir was already archived (archive-manifest.json present) nothing is touched: the archive
// is write-once, so the copies it holds are the run's meta files.
func copyMetaFiles(opts Options) error {
	if len(opts.ArchiveMetaFiles) == 0 {
		return nil
	}
	outDir := opts.OutputDir
	if fileExists(filepath.Join(outDir, runarchive.ManifestFile)) {
		log.Warnf("⚠️  %s was already archived (--step %s): options.archiveMetaFiles are not copied again "+
			"(the archive keeps the %s/ it was uploaded with)", outDir, stepUpload, dirMetaFiles)
		return nil
	}
	dir := filepath.Join(outDir, dirMetaFiles)
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	keep := make(map[string]bool, len(opts.ArchiveMetaFiles))
	for _, src := range opts.ArchiveMetaFiles {
		rel := metaFileName(src)
		if err := compressMetaFile(src, filepath.Join(outDir, rel)); err != nil {
			return fmt.Errorf("options.archiveMetaFiles: %w", err)
		}
		keep[filepath.Base(rel)] = true
		log.Infof("Meta file %s copied to %s", src, filepath.Join(outDir, rel))
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

// compressMetaFile writes src, zstd-compressed, to dst (through dst+".partial", renamed once complete).
func compressMetaFile(src, dst string) (err error) {
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
	enc, err := zstd.NewWriter(out)
	if err != nil {
		return fmt.Errorf("zstd writer for %s: %w", src, err)
	}
	if _, err := io.Copy(enc, in); err != nil {
		_ = enc.Close()
		return fmt.Errorf("compress %s: %w", src, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("compress %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, dst, err)
	}
	return nil
}
