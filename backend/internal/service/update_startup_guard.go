package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrStartupGuardInvalid     = infraerrors.ServiceUnavailable("STARTUP_GUARD_INVALID", "startup guard is unavailable or invalid; the installed program was not changed")
	ErrStartupGuardNotApproved = infraerrors.Conflict("STARTUP_GUARD_NOT_APPROVED", "this executable is not approved by the host startup guard; approve the reviewed release before updating or restarting")
	ErrRestartTargetChanged    = infraerrors.Conflict("RESTART_TARGET_CHANGED", "the installed executable changed after restart preparation; restart was canceled")
	guardAllowlistLine         = regexp.MustCompile(`^[0-9a-f]{64}(?:\|[0-9a-f]{64})*\) ;;$`)
)

const (
	startupGuardMarker  = "REVIEWED FINAL EXEC GUARD"
	startupGuardBegin   = "# BEGIN REVIEWED FINAL EXEC GUARD"
	startupGuardEnd     = "# END REVIEWED FINAL EXEC GUARD"
	maxStartupGuardSize = 256 * 1024
)

// Only the reviewed, non-evaluated final-exec scaffold is understood. Comments
// and indentation may change, but arbitrary shell branches are never treated as
// approvals. The application reads this root-owned file; it never edits it.
const reviewedGuardBeforeAllowlist = `guard_fail() {
echo "sub2api: startup guard rejected executable" >&2
exit 126
}
case "$1" in
/app/sub2api|/app/data/bin/sub2api) ;;
*) guard_fail ;;
esac
guard_path=$1
[ ! -L /app ] || guard_fail
[ ! -L /app/data ] || guard_fail
[ ! -L /app/data/bin ] || guard_fail
[ ! -L "$guard_path" ] && [ -f "$guard_path" ] && [ -x "$guard_path" ] || guard_fail
exec 9< "$guard_path" || guard_fail
[ -f /proc/self/fd/9 ] && [ -x /proc/self/fd/9 ] || guard_fail
guard_identity=$(stat -Lc '%d:%i:%s:%Y:%Z' /proc/self/fd/9) || guard_fail
guard_hash=$(sha256sum /proc/self/fd/9) || guard_fail
guard_hash=${guard_hash%% *}
case "$guard_hash" in`

const reviewedGuardAfterAllowlist = `*) guard_fail ;;
esac
[ "$(readlink /proc/self/fd/9)" = "$guard_path" ] || guard_fail
[ ! -L "$guard_path" ] || guard_fail
[ "$(stat -Lc '%d:%i:%s:%Y:%Z' /proc/self/fd/9)" = "$guard_identity" ] || guard_fail
[ "$(stat -Lc '%d:%i:%s:%Y:%Z' "$guard_path")" = "$guard_identity" ] || guard_fail
shift
exec /proc/self/fd/9 "$@"`

// SystemRestartReservation fences executable mutations from preflight until a
// synchronous self-signal. Execute must not receive an asynchronous scheduler.
type SystemRestartReservation interface {
	Execute(context.Context, func() error) error
	Cancel()
}

type systemRestartReservation struct {
	service   *UpdateService
	target    *executableSnapshot
	completed bool // protected by service.operationMu
}

type executableSnapshot struct {
	path         string
	info         os.FileInfo
	sha256       string
	dependencies []*executableSnapshot
}

func (s *UpdateService) beginExecutableOperation(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.operationMu.TryLock() {
		return ErrSystemOperationBusy
	}
	if s.restartPending != nil {
		s.operationMu.Unlock()
		return ErrSystemOperationBusy
	}
	return nil
}

func (s *UpdateService) PrepareRestart(ctx context.Context) (SystemRestartReservation, error) {
	if s == nil {
		return nil, fmt.Errorf("update service is unavailable")
	}
	if err := s.beginExecutableOperation(ctx); err != nil {
		return nil, err
	}
	defer s.operationMu.Unlock()
	path, err := s.executablePath()
	if err != nil {
		return nil, err
	}
	target, err := s.checkStartupAllowed(ctx, path)
	if err != nil {
		return nil, err
	}
	reservation := &systemRestartReservation{service: s, target: target}
	s.restartPending = reservation
	return reservation, nil
}

func (r *systemRestartReservation) Cancel() {
	if r == nil || r.service == nil {
		return
	}
	s := r.service
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if !r.completed && s.restartPending == r {
		r.completed = true
		s.restartPending = nil
	}
}

func (r *systemRestartReservation) Execute(ctx context.Context, signal func() error) error {
	if r == nil || r.service == nil {
		return ErrRestartTargetChanged
	}
	s := r.service
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if r.completed || s.restartPending != r {
		return ErrRestartTargetChanged
	}
	r.completed = true
	succeeded := false
	defer func() {
		if !succeeded {
			s.restartPending = nil
		}
	}()
	actual, err := s.checkStartupAllowed(ctx, r.target.path)
	if err != nil {
		return err
	}
	if actual.sha256 != r.target.sha256 || !sameObservedFile(actual.info, r.target.info) {
		return ErrRestartTargetChanged
	}
	if signal == nil {
		return fmt.Errorf("restart signal is unavailable")
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err := signal(); err != nil {
		return err
	}
	// SIGTERM starts graceful shutdown, not immediate exit. Keep the reservation
	// to reject new replacements throughout that shutdown window.
	succeeded = true
	return nil
}

func (s *UpdateService) checkStartupAllowed(ctx context.Context, path string) (*executableSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	validated, err := snapshotExecutableFile(path)
	if err != nil {
		return nil, fmt.Errorf("invalid restart executable: %w", err)
	}
	allowlist, guardSnapshot, ancestors, err := s.readStartupGuard(ctx)
	if err != nil {
		return nil, err
	}
	var targetAncestors []*executableSnapshot
	if guardSnapshot != nil {
		targetAncestors, err = inspectPathAncestors(path, nil)
		if err != nil {
			return nil, ErrRestartTargetChanged.WithCause(err)
		}
	}
	_, target, err := readStableFile(ctx, path, maxDownloadSize, false)
	if err != nil {
		return nil, fmt.Errorf("inspect restart executable: %w", err)
	}
	if !sameObservedFile(validated.info, target.info) {
		return nil, ErrRestartTargetChanged
	}
	if guardSnapshot != nil {
		if !allowlist[target.sha256] {
			return nil, ErrStartupGuardNotApproved
		}
		guardSnapshot.dependencies = ancestors
		if err := checkObservedPath(guardSnapshot); err != nil {
			return nil, ErrStartupGuardInvalid.WithCause(err)
		}
		target.dependencies = append(targetAncestors, guardSnapshot)
	}
	if err := checkObservedPath(target); err != nil {
		return nil, ErrRestartTargetChanged.WithCause(err)
	}
	return target, nil
}

func (s *UpdateService) readStartupGuard(ctx context.Context) (map[string]bool, *executableSnapshot, []*executableSnapshot, error) {
	path := s.startupGuardPath
	if path == "" {
		path = "/app/docker-entrypoint.sh"
	}
	body, snapshot, err := readStableFile(ctx, path, maxStartupGuardSize, true)
	if err != nil {
		if os.IsNotExist(err) && !s.startupGuardRequired && !s.startupGuardObserved {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, ErrStartupGuardInvalid.WithCause(err)
	}
	present := strings.Contains(string(body), startupGuardMarker) || strings.Contains(string(body), `case "$guard_hash"`)
	if present {
		s.startupGuardObserved = true
	}
	if !present && !s.startupGuardRequired && !s.startupGuardObserved {
		return nil, nil, nil, nil
	}
	allowlist, err := parseReviewedStartupGuard(string(body))
	if err != nil {
		return nil, nil, nil, ErrStartupGuardInvalid.WithCause(err)
	}
	trust := s.startupGuardTrust
	if trust == nil {
		trust = verifyStartupGuardOwnership
	}
	if err := trust(path, snapshot.info); err != nil {
		return nil, nil, nil, ErrStartupGuardInvalid.WithCause(err)
	}
	ancestors, err := inspectPathAncestors(path, trust)
	if err != nil {
		return nil, nil, nil, ErrStartupGuardInvalid.WithCause(err)
	}
	return allowlist, snapshot, ancestors, nil
}

func inspectPathAncestors(path string, trust func(string, os.FileInfo) error) ([]*executableSnapshot, error) {
	var ancestors []*executableSnapshot
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe executable or guard ancestor")
		}
		if trust != nil {
			if err := trust(parent, info); err != nil {
				return nil, err
			}
		}
		ancestors = append(ancestors, &executableSnapshot{path: parent, info: info})
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return ancestors, nil
}

func parseReviewedStartupGuard(body string) (map[string]bool, error) {
	invalid := func() (map[string]bool, error) { return nil, fmt.Errorf("unrecognized reviewed startup guard") }
	if strings.ContainsAny(body, "\x00\r") || strings.Count(body, startupGuardBegin) != 1 || strings.Count(body, startupGuardEnd) != 1 || strings.Count(body, `case "$guard_hash"`) != 1 {
		return invalid()
	}
	_, remainder, _ := strings.Cut(body, startupGuardBegin)
	block, tail, ok := strings.Cut(remainder, startupGuardEnd)
	if !ok || strings.TrimSpace(tail) != "" {
		return invalid()
	}
	// Marker lines must be real full lines, not quoted examples or suffix text.
	if !strings.Contains("\n"+body, "\n"+startupGuardBegin+"\n") || !strings.Contains(body, "\n"+startupGuardEnd) {
		return invalid()
	}
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	prefix, suffix := strings.Split(reviewedGuardBeforeAllowlist, "\n"), strings.Split(reviewedGuardAfterAllowlist, "\n")
	if len(lines) != len(prefix)+1+len(suffix) || strings.Join(lines[:len(prefix)], "\n") != reviewedGuardBeforeAllowlist || strings.Join(lines[len(prefix)+1:], "\n") != reviewedGuardAfterAllowlist {
		return invalid()
	}
	line := lines[len(prefix)]
	if !guardAllowlistLine.MatchString(line) {
		return invalid()
	}
	allowlist := make(map[string]bool)
	for _, hash := range strings.Split(strings.TrimSuffix(line, ") ;;"), "|") {
		if allowlist[hash] {
			return invalid()
		}
		allowlist[hash] = true
	}
	return allowlist, nil
}

func readStableFile(ctx context.Context, path string, maxSize int64, keepBody bool) ([]byte, *executableSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() < 0 || before.Size() > maxSize {
		return nil, nil, fmt.Errorf("unsafe or oversized file")
	}
	file, err := openStartupInspectionFile(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !sameObservedFile(before, opened) {
		return nil, nil, fmt.Errorf("file changed while opening")
	}
	hash := sha256.New()
	var body strings.Builder
	var writer io.Writer = hash
	if keepBody {
		writer = io.MultiWriter(hash, &body)
	}
	reader := &updateContextReader{ctx: ctx, reader: io.LimitReader(file, maxSize+1)}
	n, err := io.Copy(writer, reader)
	if err != nil {
		return nil, nil, err
	}
	if n != before.Size() {
		return nil, nil, fmt.Errorf("file size changed while reading")
	}
	after, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !sameObservedFile(before, after) {
		return nil, nil, fmt.Errorf("file changed while reading")
	}
	snapshot := &executableSnapshot{path: path, info: after, sha256: hex.EncodeToString(hash.Sum(nil))}
	if err := checkObservedPath(snapshot); err != nil {
		return nil, nil, err
	}
	return []byte(body.String()), snapshot, nil
}

type updateContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *updateContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func checkObservedPath(snapshot *executableSnapshot) error {
	for _, dependency := range snapshot.dependencies {
		if err := checkObservedPath(dependency); err != nil {
			return err
		}
	}
	actual, err := os.Lstat(snapshot.path)
	if err != nil {
		return err
	}
	if !sameObservedFile(snapshot.info, actual) {
		return fmt.Errorf("file identity changed")
	}
	return nil
}

func sameObservedFile(a, b os.FileInfo) bool {
	if a.IsDir() && b.IsDir() {
		// Unrelated sibling creation must not invalidate an ancestor. Directory
		// identity, permissions and owner changes still invalidate the snapshot.
		return os.SameFile(a, b) && a.Mode() == b.Mode() && samePlatformDirectoryMetadata(a, b)
	}
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && samePlatformFileMetadata(a, b)
}
