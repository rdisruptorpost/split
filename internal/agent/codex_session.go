package agent

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const codexLogFreshnessAllowance = 10 * time.Second

var (
	codexLogDatabasePattern = regexp.MustCompile(`(?i)^logs_(\d+)\.sqlite$`)
	codexSessionIDPattern   = regexp.MustCompile(
		`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
	)
)

// CodexSessionResolver correlates a live Codex process with the root thread ID
// that Codex records in its local diagnostic log. Codex currently defers
// SessionStart hooks until the first turn, so this read-only path closes the
// gap when a user resumes a chat and exits split before submitting a prompt.
type CodexSessionResolver struct {
	home   string
	db     *sql.DB
	dbPath string
}

func NewCodexSessionResolver() *CodexSessionResolver {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".codex")
		}
	}
	return NewCodexSessionResolverAt(home)
}

func NewCodexSessionResolverAt(home string) *CodexSessionResolver {
	home = strings.TrimSpace(home)
	if home != "" {
		home = filepath.Clean(home)
	}
	return &CodexSessionResolver{home: home}
}

// Resolve returns the first valid root thread ID logged by the current Codex
// process. The lower timestamp bound prevents a recycled Windows PID from
// matching an older Codex invocation.
func (resolver *CodexSessionResolver) Resolve(
	pid uint32,
	observedAt time.Time,
) (string, bool, error) {
	if resolver == nil || pid == 0 {
		return "", false, nil
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	if err := resolver.ensureOpen(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	freshAfter := observedAt.Add(-codexLogFreshnessAllowance)
	if processStartedAt, ok := processStartTime(pid); ok {
		freshAfter = processStartedAt.Add(-time.Second)
	}
	rows, err := resolver.db.Query(`
		SELECT thread_id
		FROM logs
		WHERE ts >= ?
			AND process_uuid GLOB ?
			AND thread_id IS NOT NULL
			AND length(trim(thread_id)) > 0
		ORDER BY ts ASC, ts_nanos ASC, id ASC
		LIMIT 32`,
		freshAfter.Unix(),
		"pid:"+strconv.FormatUint(uint64(pid), 10)+":*",
	)
	if err != nil {
		return "", false, fmt.Errorf("query Codex process thread: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return "", false, fmt.Errorf("scan Codex process thread: %w", err)
		}
		sessionID = strings.ToLower(strings.TrimSpace(sessionID))
		if codexSessionIDPattern.MatchString(sessionID) {
			return sessionID, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterate Codex process threads: %w", err)
	}
	return "", false, nil
}

func (resolver *CodexSessionResolver) Close() error {
	if resolver == nil || resolver.db == nil {
		return nil
	}
	err := resolver.db.Close()
	resolver.db = nil
	resolver.dbPath = ""
	return err
}

func (resolver *CodexSessionResolver) ensureOpen() error {
	if resolver.db != nil {
		return nil
	}
	path, err := latestCodexLogDatabase(resolver.home)
	if err != nil {
		return err
	}

	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(500)")
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: query.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open Codex diagnostic log: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("read Codex diagnostic log: %w", err)
	}
	resolver.db = db
	resolver.dbPath = path
	return nil
}

func latestCodexLogDatabase(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", os.ErrNotExist
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return "", err
	}
	type candidate struct {
		path    string
		version int
	}
	candidates := make([]candidate, 0, 2)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := codexLogDatabasePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{
			path:    filepath.Join(home, entry.Name()),
			version: version,
		})
	}
	if len(candidates) == 0 {
		return "", os.ErrNotExist
	}
	slices.SortFunc(candidates, func(left, right candidate) int {
		return right.version - left.version
	})
	return candidates[0].path, nil
}
