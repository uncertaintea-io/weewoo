package collection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ECDFOutputDirConfigKey = "ecdf_output_dir"
	defaultECDFOutputDir   = "ecdfs"

	ecdfManifestFile         = "joint-ecdf.manifest.json"
	ecdfRecoveryManifestFile = "joint-ecdf.recovery-manifest.json"
	ecdfLockFile             = "joint-ecdf.lock.json"
	ecdfUploadFile           = "joint-ecdf.uploading"
	ecdfVersionFileFormat    = "joint-ecdf-%06d.bin"

	ecdfManifestSchemaVersion = 1
	ecdfRetainedVersions      = 5
	ecdfManifestLockTTL       = 15 * time.Minute

	ecdfManifestStatusComplete = "complete"
	ecdfManifestStatusRecovery = "recovery"
)

type jointECDFFileStore struct {
	dir         string
	serviceID   int
	indicatorID int
}

type jointECDFManifest struct {
	SchemaVersion int                `json:"schema_version"`
	ServiceID     int                `json:"service_id"`
	IndicatorID   int                `json:"indicator_id"`
	Status        string             `json:"status"`
	Current       *jointECDFFileInfo `json:"current,omitempty"`
	Previous      *jointECDFFileInfo `json:"previous,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type jointECDFFileInfo struct {
	Version   int       `json:"version"`
	File      string    `json:"file"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type jointECDFManifestLock struct {
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type countingHashWriter struct {
	writer io.Writer
	hash   hash.Hash
	bytes  int64
}

func newJointECDFFileStore(root string, serviceID int, indicatorID int) jointECDFFileStore {
	if root == "" {
		root = defaultECDFOutputDir
	}
	return jointECDFFileStore{
		dir:         filepath.Join(root, fmt.Sprintf("service-%d", serviceID), fmt.Sprintf("indicator-%d", indicatorID)),
		serviceID:   serviceID,
		indicatorID: indicatorID,
	}
}

func ReadCurrentJointECDF(root string, serviceID int, indicatorID int) ([]byte, error) {
	store := newJointECDFFileStore(root, serviceID, indicatorID)
	return store.readCurrent()
}

func RecoverJointECDFOutputDir(root string) error {
	if root == "" {
		root = defaultECDFOutputDir
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "indicator-") {
			return nil
		}
		serviceID, indicatorID, ok := parseJointECDFStoreDir(path)
		if !ok {
			return nil
		}
		store := jointECDFFileStore{dir: path, serviceID: serviceID, indicatorID: indicatorID}
		return store.recover()
	})
}

func parseJointECDFStoreDir(path string) (int, int, bool) {
	var serviceID int
	var indicatorID int
	if _, err := fmt.Sscanf(filepath.Base(path), "indicator-%d", &indicatorID); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(filepath.Base(filepath.Dir(path)), "service-%d", &serviceID); err != nil {
		return 0, 0, false
	}
	return serviceID, indicatorID, true
}

func (s jointECDFFileStore) manifestPath() string {
	return filepath.Join(s.dir, ecdfManifestFile)
}

func (s jointECDFFileStore) recoveryManifestPath() string {
	return filepath.Join(s.dir, ecdfRecoveryManifestFile)
}

func (s jointECDFFileStore) lockPath() string {
	return filepath.Join(s.dir, ecdfLockFile)
}

func (s jointECDFFileStore) uploadPath() string {
	return filepath.Join(s.dir, ecdfUploadFile)
}

func (s jointECDFFileStore) versionPath(version int) string {
	return filepath.Join(s.dir, fmt.Sprintf(ecdfVersionFileFormat, version))
}

func (s jointECDFFileStore) outputPath() string {
	return s.manifestPath()
}

func (s jointECDFFileStore) recoveryPath() string {
	return s.recoveryManifestPath()
}

func (s jointECDFFileStore) withManifestLock(fn func() error) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("failed to create ECDF output directory: %w", err)
	}
	if err := s.acquireManifestLock(); err != nil {
		return err
	}
	defer s.releaseManifestLock()
	return fn()
}

func (s jointECDFFileStore) acquireManifestLock() error {
	now := time.Now().UTC()
	lock := jointECDFManifestLock{
		PID:       os.Getpid(),
		CreatedAt: now,
		ExpiresAt: now.Add(ecdfManifestLockTTL),
	}
	body, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode ECDF manifest lock: %w", err)
	}

	for {
		file, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			if _, err := file.Write(body); err != nil {
				_ = file.Close()
				_ = os.Remove(s.lockPath())
				return fmt.Errorf("failed to write ECDF manifest lock: %w", err)
			}
			if err := file.Sync(); err != nil {
				_ = file.Close()
				_ = os.Remove(s.lockPath())
				return fmt.Errorf("failed to sync ECDF manifest lock: %w", err)
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(s.lockPath())
				return fmt.Errorf("failed to close ECDF manifest lock: %w", err)
			}
			return syncDir(s.dir)
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("failed to create ECDF manifest lock: %w", err)
		}
		stale, err := s.manifestLockIsStale(now)
		if err != nil {
			return err
		}
		if !stale {
			return fmt.Errorf("ECDF manifest is locked")
		}
		if err := os.Remove(s.lockPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove stale ECDF manifest lock: %w", err)
		}
	}
}

func (s jointECDFFileStore) manifestLockIsStale(now time.Time) (bool, error) {
	body, err := os.ReadFile(s.lockPath())
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read ECDF manifest lock: %w", err)
	}
	var lock jointECDFManifestLock
	if err := json.Unmarshal(body, &lock); err != nil {
		return true, nil
	}
	return !lock.ExpiresAt.After(now), nil
}

func (s jointECDFFileStore) releaseManifestLock() {
	_ = os.Remove(s.lockPath())
	_ = syncDir(s.dir)
}

func (s jointECDFFileStore) recover() error {
	return s.withManifestLock(s.recoverLocked)
}

func (s jointECDFFileStore) recoverLocked() error {
	_ = os.Remove(s.uploadPath())

	manifest, _, err := s.readOptionalManifest(s.manifestPath())
	if err != nil {
		return err
	}
	recovery, _, err := s.readOptionalManifest(s.recoveryManifestPath())
	if err != nil {
		return err
	}

	current := s.validEntry(manifest.Current)
	previous := s.validEntry(manifest.Previous)
	recoveryCurrent := s.validEntry(recovery.Current)

	// The recovery manifest exists for crashes between "version file complete"
	// and "main manifest committed". If it points at a newer valid version,
	// finish that interrupted commit before readers see stale data.
	if recoveryCurrent != nil && (current == nil || recoveryCurrent.Version > current.Version) {
		if err := s.writeCurrentManifest(recoveryCurrent, currentOrPrevious(current, previous)); err != nil {
			return err
		}
		_ = os.Remove(s.recoveryManifestPath())
		return s.cleanupOldVersions()
	}

	if current != nil {
		_ = os.Remove(s.recoveryManifestPath())
		return s.cleanupOldVersions()
	}
	if previous != nil {
		if err := s.writeCurrentManifest(previous, nil); err != nil {
			return err
		}
		_ = os.Remove(s.recoveryManifestPath())
		return s.cleanupOldVersions()
	}

	scanned, err := s.newestValidVersionFromDisk()
	if err != nil {
		return err
	}
	if scanned != nil {
		if err := s.writeCurrentManifest(scanned, nil); err != nil {
			return err
		}
		return s.cleanupOldVersions()
	}
	return nil
}

func currentOrPrevious(current *jointECDFFileInfo, previous *jointECDFFileInfo) *jointECDFFileInfo {
	if current != nil {
		return current
	}
	return previous
}

func (s jointECDFFileStore) publish(build func(io.Writer) error) (int64, error) {
	var bytesWritten int64
	err := s.withManifestLock(func() error {
		if err := s.recoverLocked(); err != nil {
			return err
		}
		written, err := s.writeRecoveryLocked(build)
		if err != nil {
			return err
		}
		bytesWritten = written
		return s.promoteRecoveryLocked()
	})
	return bytesWritten, err
}

func (s jointECDFFileStore) writeRecovery(build func(io.Writer) error) (int64, error) {
	var bytesWritten int64
	err := s.withManifestLock(func() error {
		if err := s.recoverLocked(); err != nil {
			return err
		}
		written, err := s.writeRecoveryLocked(build)
		bytesWritten = written
		return err
	})
	return bytesWritten, err
}

func (s jointECDFFileStore) writeRecoveryLocked(build func(io.Writer) error) (int64, error) {
	manifest, _, err := s.readOptionalManifest(s.manifestPath())
	if err != nil {
		return 0, err
	}
	nextVersion := manifest.highestVersion() + 1
	if diskVersion, err := s.highestVersionOnDisk(); err != nil {
		return 0, err
	} else if diskVersion >= nextVersion {
		nextVersion = diskVersion + 1
	}

	_ = os.Remove(s.uploadPath())
	file, err := os.OpenFile(s.uploadPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to create ECDF upload file: %w", err)
	}

	counter := &countingHashWriter{writer: file, hash: sha256.New()}
	if err := build(counter); err != nil {
		_ = file.Close()
		_ = os.Remove(s.uploadPath())
		return 0, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(s.uploadPath())
		return 0, fmt.Errorf("failed to sync ECDF upload file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(s.uploadPath())
		return 0, fmt.Errorf("failed to close ECDF upload file: %w", err)
	}
	if counter.bytes == 0 {
		_ = os.Remove(s.uploadPath())
		return 0, fmt.Errorf("built ECDF is empty")
	}

	entry := &jointECDFFileInfo{
		Version:   nextVersion,
		File:      filepath.Base(s.versionPath(nextVersion)),
		Bytes:     counter.bytes,
		SHA256:    hex.EncodeToString(counter.hash.Sum(nil)),
		CreatedAt: time.Now().UTC(),
	}
	if err := os.Rename(s.uploadPath(), s.versionPath(nextVersion)); err != nil {
		_ = os.Remove(s.uploadPath())
		return 0, fmt.Errorf("failed to publish ECDF version file: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return 0, err
	}

	// The recovery manifest is a breadcrumb for startup recovery. If the process
	// dies before the main manifest below is written, recovery can still verify
	// and promote this complete version file.
	if err := s.writeRecoveryManifest(entry, s.validEntry(manifest.Current)); err != nil {
		return 0, err
	}
	return counter.bytes, nil
}

func (s jointECDFFileStore) promoteRecovery() error {
	return s.withManifestLock(func() error {
		return s.promoteRecoveryLocked()
	})
}

func (s jointECDFFileStore) promoteRecoveryLocked() error {
	manifest, _, err := s.readOptionalManifest(s.manifestPath())
	if err != nil {
		return err
	}
	recovery, err := s.readManifest(s.recoveryManifestPath())
	if err != nil {
		return err
	}
	recoveryCurrent := s.validEntry(recovery.Current)
	if recoveryCurrent == nil {
		return fmt.Errorf("ECDF recovery manifest does not point at a valid version")
	}
	// This manifest write is the commit point. Readers only trust this file,
	// so a crash before here leaves the old current version in place.
	if err := s.writeCurrentManifest(recoveryCurrent, currentOrPrevious(s.validEntry(manifest.Current), s.validEntry(manifest.Previous))); err != nil {
		return err
	}
	_ = os.Remove(s.recoveryManifestPath())
	return s.cleanupOldVersions()
}

func (s jointECDFFileStore) readCurrent() ([]byte, error) {
	if err := s.recover(); err != nil {
		return nil, err
	}
	manifest, err := s.readManifest(s.manifestPath())
	if err != nil {
		return nil, err
	}
	current := s.validEntry(manifest.Current)
	if current == nil {
		return nil, fmt.Errorf("ECDF manifest does not point at a valid current version")
	}
	body, err := os.ReadFile(filepath.Join(s.dir, current.File))
	if err != nil {
		return nil, fmt.Errorf("failed to read current ECDF file: %w", err)
	}
	if int64(len(body)) != current.Bytes {
		return nil, fmt.Errorf("current ECDF file size mismatch")
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != current.SHA256 {
		return nil, fmt.Errorf("current ECDF file checksum mismatch")
	}
	return body, nil
}

func (s jointECDFFileStore) readManifest(path string) (jointECDFManifest, error) {
	var manifest jointECDFManifest
	body, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return manifest, fmt.Errorf("failed to decode ECDF manifest %s: %w", filepath.Base(path), err)
	}
	return manifest, nil
}

func (s jointECDFFileStore) readOptionalManifest(path string) (jointECDFManifest, bool, error) {
	manifest, err := s.readManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return jointECDFManifest{}, false, nil
	}
	if err != nil {
		return jointECDFManifest{}, false, err
	}
	return manifest, true, nil
}

func (s jointECDFFileStore) writeCurrentManifest(current *jointECDFFileInfo, previous *jointECDFFileInfo) error {
	return s.writeManifest(s.newManifest(ecdfManifestStatusComplete, current, previous), s.manifestPath())
}

func (s jointECDFFileStore) writeRecoveryManifest(current *jointECDFFileInfo, previous *jointECDFFileInfo) error {
	return s.writeManifest(s.newManifest(ecdfManifestStatusRecovery, current, previous), s.recoveryManifestPath())
}

func (s jointECDFFileStore) newManifest(status string, current *jointECDFFileInfo, previous *jointECDFFileInfo) jointECDFManifest {
	return jointECDFManifest{
		SchemaVersion: ecdfManifestSchemaVersion,
		ServiceID:     s.serviceID,
		IndicatorID:   s.indicatorID,
		Status:        status,
		Current:       current,
		Previous:      previous,
		UpdatedAt:     time.Now().UTC(),
	}
}

func (s jointECDFFileStore) writeManifest(manifest jointECDFManifest, path string) error {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode ECDF manifest: %w", err)
	}
	body = append(body, '\n')
	tmp := path + ".uploading"
	_ = os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to create ECDF manifest upload file: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to write ECDF manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to sync ECDF manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to close ECDF manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to publish ECDF manifest: %w", err)
	}
	return syncDir(s.dir)
}

func (m jointECDFManifest) highestVersion() int {
	highest := 0
	if m.Current != nil && m.Current.Version > highest {
		highest = m.Current.Version
	}
	if m.Previous != nil && m.Previous.Version > highest {
		highest = m.Previous.Version
	}
	return highest
}

func (s jointECDFFileStore) validEntry(entry *jointECDFFileInfo) *jointECDFFileInfo {
	if entry == nil || entry.File == "" || entry.Bytes <= 0 || entry.SHA256 == "" {
		return nil
	}
	if filepath.Base(entry.File) != entry.File {
		return nil
	}
	path := filepath.Join(s.dir, entry.File)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Bytes {
		return nil
	}
	sum, err := fileSHA256(path)
	if err != nil || sum != entry.SHA256 {
		return nil
	}
	copy := *entry
	return &copy
}

func (s jointECDFFileStore) highestVersionOnDisk() (int, error) {
	versions, err := s.versionFiles()
	if err != nil {
		return 0, err
	}
	if len(versions) == 0 {
		return 0, nil
	}
	return versions[len(versions)-1], nil
}

func (s jointECDFFileStore) newestValidVersionFromDisk() (*jointECDFFileInfo, error) {
	versions, err := s.versionFiles()
	if err != nil {
		return nil, err
	}
	for i := len(versions) - 1; i >= 0; i-- {
		path := s.versionPath(versions[i])
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		sum, err := fileSHA256(path)
		if err != nil {
			continue
		}
		return &jointECDFFileInfo{
			Version:   versions[i],
			File:      filepath.Base(path),
			Bytes:     info.Size(),
			SHA256:    sum,
			CreatedAt: info.ModTime().UTC(),
		}, nil
	}
	return nil, nil
}

func (s jointECDFFileStore) versionFiles() ([]int, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read ECDF output directory: %w", err)
	}
	versions := make([]int, 0)
	for _, entry := range entries {
		if version, ok := parseVersionFileName(entry.Name()); ok {
			versions = append(versions, version)
		}
	}
	sort.Ints(versions)
	return versions, nil
}

func parseVersionFileName(name string) (int, bool) {
	if !strings.HasPrefix(name, "joint-ecdf-") || !strings.HasSuffix(name, ".bin") {
		return 0, false
	}
	versionText := strings.TrimSuffix(strings.TrimPrefix(name, "joint-ecdf-"), ".bin")
	if len(versionText) != 6 {
		return 0, false
	}
	version, err := strconv.Atoi(versionText)
	return version, err == nil
}

func (s jointECDFFileStore) cleanupOldVersions() error {
	manifest, err := s.readManifest(s.manifestPath())
	if err != nil {
		return err
	}
	versions, err := s.versionFiles()
	if err != nil {
		return err
	}
	keep := map[int]bool{}
	if manifest.Current != nil {
		keep[manifest.Current.Version] = true
	}
	if manifest.Previous != nil {
		keep[manifest.Previous.Version] = true
	}
	for i := len(versions) - 1; i >= 0 && len(keep) < ecdfRetainedVersions; i-- {
		keep[versions[i]] = true
	}
	for _, version := range versions {
		if keep[version] {
			continue
		}
		if err := os.Remove(s.versionPath(version)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove old ECDF version file: %w", err)
		}
	}
	return syncDir(s.dir)
}

func (w *countingHashWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		_, _ = w.hash.Write(p[:n])
		w.bytes += int64(n)
	}
	return n, err
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open ECDF output directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("failed to sync ECDF output directory: %w", err)
	}
	return nil
}
