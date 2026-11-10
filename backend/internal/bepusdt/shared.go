package bepusdt

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lib/pq"
	beconf "github.com/v03413/bepusdt/app/conf"
	belog "github.com/v03413/bepusdt/app/log"
	"github.com/v03413/bepusdt/app/model"
	"github.com/v03413/bepusdt/app/task"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const scannerLock int64 = 0x5355423242455001
const allocationLock int64 = 0x5355423242455002
const sharedSchema model.ConfKey = "sub2api_shared_schema"
const sharedEnabled model.ConfKey = "sub2api_scanner_requested"

var errSharedUnavailable = errors.New("built-in payment worker is temporarily unavailable")
var errScannerFence = errors.New("built-in payment scanner session is fenced")
var startSharedScanner = task.Start
var waitSharedScanner = task.Wait
var sharedTermResult = func(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		belog.Warn("shared payment worker unavailable; retrying after task drain")
	}
}

// A term admits exactly one connection. database/sql must not transparently
// reconnect a scanner after losing its lock: the supervisor first cancels and
// joins every old task and notification, then explicitly arms the next term.
type scannerConnector struct {
	base  driver.Connector
	mu    sync.Mutex
	armed bool
}

func (c *scannerConnector) Driver() driver.Driver { return c.base.Driver() }
func (c *scannerConnector) arm()                  { c.mu.Lock(); c.armed = true; c.mu.Unlock() }
func (c *scannerConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.armed {
		return nil, errScannerFence
	}
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, errSharedUnavailable
	}
	query, ok := conn.(driver.QueryerContext)
	if !ok {
		_ = conn.Close()
		return nil, errScannerFence
	}
	rows, err := query.QueryContext(ctx, "SELECT NOT pg_is_in_recovery() AND pg_try_advisory_lock($1)", []driver.NamedValue{{Ordinal: 1, Value: scannerLock}})
	if err != nil {
		_ = conn.Close()
		return nil, errSharedUnavailable
	}
	values := make([]driver.Value, 1)
	err = rows.Next(values)
	_ = rows.Close()
	if err != nil || values[0] != true {
		_ = conn.Close()
		return nil, errScannerFence
	}
	c.armed = false
	return conn, nil
}

var shared struct {
	enabled   bool
	worker    bool
	db        *sql.DB
	connector *scannerConnector
	cancel    context.CancelFunc
	done      chan struct{}
}

// ConfigureSharedPostgres is called once before dependency construction. The
// explicit HA pair takes precedence over the ordinary single-process role.
// Existing SQLite data requires the documented offline import; never fork it.
func ConfigureSharedPostgres(dsn string, worker bool) error {
	runtime.Lock()
	defer runtime.Unlock()
	if runtime.ready || shared.enabled {
		return errors.New("built-in payment runtime is already configured")
	}
	role, err := sharedWorkerRole(worker, os.Getenv("SUB2API_WORKER_ONLY"), os.Getenv("SUB2API_BACKGROUND_MODE"))
	if err != nil {
		return err
	}
	if dsn == "" {
		return errors.New("shared payment database is not configured")
	}
	for _, name := range []string{"sqlite.db", "sqlite.db-wal"} {
		if info, err := os.Stat(filepath.Join(DataDir(), name)); err == nil && info.Size() > 0 {
			return errors.New("existing built-in payment SQLite data requires verified offline import before shared PostgreSQL can be enabled")
		} else if err != nil && !os.IsNotExist(err) {
			return errors.New("cannot inspect existing built-in payment data")
		}
	}
	base, err := pq.NewConnector(dsn)
	if err != nil {
		return errors.New("invalid shared payment database configuration")
	}
	if err := os.MkdirAll(filepath.Join(DataDir(), "logs"), 0700); err != nil {
		return err
	}
	if err := belog.Init(filepath.Join(DataDir(), "logs")); err != nil {
		return err
	}
	var connector driver.Connector = base
	var fence *scannerConnector
	if role {
		fence = &scannerConnector{base: base}
		connector = fence
	}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(8)
	pool.SetMaxIdleConns(2)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	if role {
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pool.SetConnMaxIdleTime(0)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool, PreferSimpleProtocol: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		_ = pool.Close()
		return errors.New("cannot initialize shared payment database")
	}
	model.Db = db
	shared.enabled, shared.worker, shared.db, shared.connector = true, role, pool, fence
	probeOnCreate.Store(true)
	return nil
}

func sharedWorkerRole(fallback bool, worker, background string) (bool, error) {
	if worker == "" && background == "" {
		return fallback, nil
	}
	if worker == "true" && background == "active" {
		return true, nil
	}
	if worker == "false" && background == "disabled" {
		return false, nil
	}
	return false, errors.New("inconsistent shared payment HA role configuration")
}

type sharedStatus struct {
	ID        int `gorm:"primaryKey"`
	UpdatedAt time.Time
	Active    bool
	Stats     string `gorm:"type:text;not null"`
}

func (sharedStatus) TableName() string { return "bep_runtime_status" }

func ensureShared() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var schema model.Conf
	if err := model.Db.WithContext(ctx).Where("k = ? AND v = ?", sharedSchema, "1").First(&schema).Error; err != nil {
		return errSharedUnavailable
	}
	if err := model.RefreshCWithDB(model.Db.WithContext(ctx)); err != nil {
		return errSharedUnavailable
	}
	return nil
}

func resumeShared() error {
	if !shared.worker || shared.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	shared.cancel, shared.done = cancel, make(chan struct{})
	go func() { defer close(shared.done); sharedLoop(ctx) }()
	return nil
}

func sharedLoop(ctx context.Context) {
	for ctx.Err() == nil {
		shared.db.SetMaxIdleConns(0) // close the previous session after its tasks joined
		shared.db.SetMaxIdleConns(1)
		shared.connector.arm()
		termCtx, cancel := context.WithCancel(ctx)
		err := sharedTerm(termCtx)
		cancel()
		waitSharedScanner()
		statusCtx, statusCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = model.Db.WithContext(statusCtx).Model(&sharedStatus{}).Where("id = ?", 1).Updates(map[string]any{"active": false, "updated_at": time.Now().UTC()}).Error
		statusCancel()
		sharedTermResult(err) // operational state is reported by the expiring shared heartbeat
		if ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	// No lock release or pool close precedes the completion of callbacks.
	shared.db.SetMaxIdleConns(0)
}

func sharedTerm(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := shared.db.PingContext(probeCtx)
	cancel()
	if err != nil {
		return err
	}
	if err := initializeSharedSchema(); err != nil {
		return err
	}
	if err := task.Init(); err != nil {
		return err
	}
	active := false
	previousSettings := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := model.RefreshCWithDB(model.Db.WithContext(queryCtx))
		cancel()
		if err != nil {
			return err
		}
		settings := make(map[string]string)
		for _, key := range SettingKeys {
			settings[string(key)] = model.GetC(key)
		}
		encoded, _ := json.Marshal(settings)
		if string(encoded) != previousSettings {
			invalidateNodeChecks()
			previousSettings = string(encoded)
		}
		if !active && model.GetC(sharedEnabled) == "1" {
			startSharedScanner(ctx)
			active = true
		}
		stats, err := json.Marshal(beconf.GetStats())
		if err != nil {
			return err
		}
		queryCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		err = model.Db.WithContext(queryCtx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&sharedStatus{ID: 1, UpdatedAt: time.Now().UTC(), Active: active, Stats: string(stats)}).Error
		cancel()
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func initializeSharedSchema() error {
	// The version marker is committed last. A completed schema must not run
	// AutoMigrate again on each leadership term (or every scanner reconnect).
	var marker model.Conf
	if err := model.Db.Where("k = ?", sharedSchema).First(&marker).Error; err == nil {
		if marker.V != "1" {
			return errors.New("unsupported shared payment schema version")
		}
		return nil
	}
	if err := model.AutoMigrate(); err != nil {
		return err
	}
	if err := model.Db.AutoMigrate(&sharedStatus{}); err != nil {
		return err
	}
	if err := model.FillDefaultConfChecked(); err != nil {
		return err
	}
	if err := model.MigratePublicRPCDefaults(); err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	return model.Db.Transaction(func(db *gorm.DB) error {
		for _, row := range []model.Conf{{K: model.BlockOffsetConfirm, V: "1"}, {K: model.ApiAuthToken, V: hex.EncodeToString(secret)}} {
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		// An old empty token may be repaired exactly once; an existing token is stable.
		if err := db.Model(&model.Conf{}).Where("k = ? AND v = ''", model.ApiAuthToken).Update("v", hex.EncodeToString(secret)).Error; err != nil {
			return err
		}
		if err := db.Model(&model.Conf{}).Where("k = ?", model.BlockOffsetConfirm).Update("v", "1").Error; err != nil {
			return err
		}
		return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Conf{K: sharedSchema, V: "1"}).Error
	})
}

func sharedSnapshot(ctx context.Context) (sharedStatus, error) {
	var status sharedStatus
	err := model.Db.WithContext(ctx).First(&status, 1).Error
	if err != nil || status.UpdatedAt.Before(time.Now().Add(-15*time.Second)) {
		return status, errSharedUnavailable
	}
	return status, nil
}

func sharedStats() map[string]beconf.Info {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, err := sharedSnapshot(ctx)
	stats := make(map[string]beconf.Info)
	if err == nil && status.Active {
		_ = json.Unmarshal([]byte(status.Stats), &stats)
	} else {
		for network := range model.GetAllNetwork() {
			stats[network] = beconf.Info{Status: "error", Error: "Payment scanner is unavailable or not configured"}
		}
	}
	return stats
}

func lockAllocation(db *gorm.DB) error {
	if !shared.enabled {
		return nil
	}
	return db.Exec("SELECT pg_advisory_xact_lock(?)", allocationLock).Error
}
