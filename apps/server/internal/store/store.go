package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

// Device mirrors a Ali MDM-enrolled tablet.
type Device struct {
	ID           string
	Name         string
	GroupID      string
	APIKeyHash   string
	// LastAppliedHash is the config hash the device last confirmed it applied.
	// The group's current hash differs from this => we must push config again.
	LastAppliedHash string
	ConfigVersion   int
	Battery         int
	Wifi            int
	AndroidVer      string
	Model          string
	LastSeen       string
	CreatedAt      string
}

// Group holds a Ali MDM structured config shared by a set of devices.
type Group struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Config        string `json:"config"` // Ali MDM {general,display,security,advanced} JSON
	ConfigHash    string `json:"config_hash"`
	ConfigVersion int    `json:"config_version"`
}

// Command is a queued action for a device (Ali MDM command types).
type Command struct {
	ID        string
	DeviceID  string
	Type      string
	Params    string // JSON
	Status    string // pending|sent|success|error
	Result    string // JSON
	ErrMsg    string
	CreatedAt string
	ExpiresAt string
}

// APKUpdate is a queued install_apk for a device.
type APKUpdate struct {
	CommandID   string
	DeviceID    string
	PackageName string
	VersionName string
	APKPath     string
	Status      string // pending|sent|success|error
}

// Roles an operator account can hold. Admins additionally manage accounts.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

// ValidRole reports whether r is a role we recognise.
func ValidRole(r string) bool { return r == RoleAdmin || r == RoleOperator }

type Operator struct {
	Email        string `json:"email"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	PasswordHash string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS devices(
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		group_id TEXT NOT NULL DEFAULT 'default',
		api_key_hash TEXT NOT NULL,
		last_applied_hash TEXT NOT NULL DEFAULT '',
		config_version INTEGER NOT NULL DEFAULT 0,
		battery INTEGER NOT NULL DEFAULT 0,
		wifi INTEGER NOT NULL DEFAULT 0,
		android_ver TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		last_seen TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS groups(
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		config TEXT NOT NULL,
		config_hash TEXT NOT NULL,
		config_version INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE IF NOT EXISTS commands(
		id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		type TEXT NOT NULL,
		params TEXT NOT NULL DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'pending',
		result TEXT NOT NULL DEFAULT '',
		err_msg TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_commands_device ON commands(device_id, status);
	CREATE TABLE IF NOT EXISTS apk_updates(
		command_id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		package_name TEXT NOT NULL DEFAULT '',
		version_name TEXT NOT NULL DEFAULT '',
		apk_path TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending'
	);
	CREATE TABLE IF NOT EXISTS operators(
		email TEXT PRIMARY KEY,
		password_hash TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS apks(
		name TEXT PRIMARY KEY,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL,
		path TEXT NOT NULL
	);
	-- The agent (Ali MDM itself) is kept apart from the managed-app catalogue.
	-- Replacing the agent kills the process mid-install, so its rollout needs
	-- durable per-device state that survives that, which apk_updates does not
	-- model. Exactly one release is current, hence the single-row constraint.
	CREATE TABLE IF NOT EXISTS agent_release(
		id INTEGER PRIMARY KEY CHECK (id = 1),
		version_code INTEGER NOT NULL,
		version_name TEXT NOT NULL DEFAULT '',
		file_name TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL,
		created_at TEXT NOT NULL
	);
	-- Whether a device is currently the subject of an unresolved offline alert.
	-- Persisted so a server restart does not re-alert a fleet that was already
	-- reported, and so recovery can be reported exactly once.
	-- Server-wide settings an operator can change from the console, so that
	-- reconfiguring alerting does not mean editing an env file and redeploying.
	CREATE TABLE IF NOT EXISTS settings(
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS device_alerts(
		device_id TEXT PRIMARY KEY,
		state TEXT NOT NULL DEFAULT 'ok',
		notified_at TEXT NOT NULL DEFAULT ''
	);
	-- Files pushed from the console to every tablet's inbox folder.
	CREATE TABLE IF NOT EXISTS files(
		name TEXT PRIMARY KEY,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL,
		uploaded_at TEXT NOT NULL DEFAULT ''
	);
	-- One row per (file, device). Delivery is per-device so the console can show
	-- which tablets actually took the file rather than assuming a push worked.
	CREATE TABLE IF NOT EXISTS file_deliveries(
		id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		file_name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_file_delivery ON file_deliveries(device_id, file_name);
	CREATE TABLE IF NOT EXISTS events(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at TEXT NOT NULL,
		kind TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT 'info',
		actor TEXT NOT NULL DEFAULT '',
		device_id TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_events_id ON events(id DESC);
	-- Transition state for the event log, kept apart from device_alerts on
	-- purpose: that one only advances when a webhook delivery succeeds, so
	-- reusing it would drop events whenever alerting was unconfigured or down.
	CREATE TABLE IF NOT EXISTS device_event_state(
		device_id TEXT PRIMARY KEY,
		state TEXT NOT NULL DEFAULT 'ok'
	);
	CREATE TABLE IF NOT EXISTS agent_updates(
		device_id TEXT PRIMARY KEY,
		target_version_code INTEGER NOT NULL,
		status TEXT NOT NULL DEFAULT 'queued',
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// `operators` shipped with only (email, password_hash). Add the later columns
	// in place so existing deployments keep their logins across an upgrade.
	for _, c := range []struct{ name, ddl string }{
		{"name", `ALTER TABLE operators ADD COLUMN name TEXT NOT NULL DEFAULT ''`},
		{"role", `ALTER TABLE operators ADD COLUMN role TEXT NOT NULL DEFAULT 'operator'`},
		{"created_at", `ALTER TABLE operators ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(db, "operators", c.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(c.ddl); err != nil {
				return err
			}
		}
	}

	// Accounts that predate roles were effectively superusers. Promote them if
	// the upgrade would otherwise leave nobody able to manage accounts.
	var admins int
	if err := db.QueryRow(`SELECT COUNT(*) FROM operators WHERE role=?`, RoleAdmin).Scan(&admins); err != nil {
		return err
	}
	if admins == 0 {
		if _, err := db.Exec(`UPDATE operators SET role=?`, RoleAdmin); err != nil {
			return err
		}
	}
	return nil
}

func hasColumn(db *sql.DB, table, col string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&n)
	return n > 0, err
}

// ── Devices ──────────────────────────────────────────────────────────────────

func (s *Store) CreateDevice(d *Device) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO devices(id,name,group_id,api_key_hash,last_applied_hash,config_version,battery,wifi,android_ver,model,last_seen,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.GroupID, d.APIKeyHash, d.LastAppliedHash, d.ConfigVersion,
		d.Battery, d.Wifi, d.AndroidVer, d.Model, d.LastSeen, d.CreatedAt)
	return err
}

func (s *Store) GetDevice(id string) (*Device, error) {
	row := s.db.QueryRow(`SELECT id,name,group_id,api_key_hash,last_applied_hash,config_version,battery,wifi,android_ver,model,last_seen,created_at FROM devices WHERE id=?`, id)
	var d Device
	if err := row.Scan(&d.ID, &d.Name, &d.GroupID, &d.APIKeyHash, &d.LastAppliedHash, &d.ConfigVersion, &d.Battery, &d.Wifi, &d.AndroidVer, &d.Model, &d.LastSeen, &d.CreatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id,name,group_id,api_key_hash,last_applied_hash,config_version,battery,wifi,android_ver,model,last_seen,created_at FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.GroupID, &d.APIKeyHash, &d.LastAppliedHash, &d.ConfigVersion, &d.Battery, &d.Wifi, &d.AndroidVer, &d.Model, &d.LastSeen, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// UpdateHeartbeat records telemetry + the config hash the device just applied.
func (s *Store) UpdateHeartbeat(id, appliedHash string, battery, wifi int, androidVer, model, lastSeen string) error {
	_, err := s.db.Exec(`UPDATE devices SET last_applied_hash=?, battery=?, wifi=?, android_ver=?, model=?, last_seen=? WHERE id=?`,
		appliedHash, battery, wifi, androidVer, model, lastSeen, id)
	return err
}

// ── Groups ───────────────────────────────────────────────────────────────────

func (s *Store) UpsertGroup(g *Group) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO groups(id,name,config,config_hash,config_version) VALUES(?,?,?,?,?)`,
		g.ID, g.Name, g.Config, g.ConfigHash, g.ConfigVersion)
	return err
}

func (s *Store) GetGroup(id string) (*Group, error) {
	row := s.db.QueryRow(`SELECT id,name,config,config_hash,config_version FROM groups WHERE id=?`, id)
	var g Group
	if err := row.Scan(&g.ID, &g.Name, &g.Config, &g.ConfigHash, &g.ConfigVersion); err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id,name,config,config_hash,config_version FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Config, &g.ConfigHash, &g.ConfigVersion); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// DeleteGroup removes a group. Callers must reassign its devices first.
func (s *Store) DeleteGroup(id string) error {
	_, err := s.db.Exec(`DELETE FROM groups WHERE id=?`, id)
	return err
}

// CountDevicesInGroup returns how many devices are assigned to a group.
func (s *Store) CountDevicesInGroup(groupID string) int {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE group_id=?`, groupID).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}

// SetDeviceGroup moves a device to a different group and invalidates its
// applied-config hash so it re-syncs the new group's policy on next heartbeat.
func (s *Store) SetDeviceGroup(deviceID, groupID string) error {
	_, err := s.db.Exec(`UPDATE devices SET group_id=?, last_applied_hash='' WHERE id=?`, groupID, deviceID)
	return err
}

// DeleteDevice removes a device and everything keyed to it.
//
// This is the server half of a forced unenrolment: it revokes the device's API
// key by destroying the only record of it, so the tablet's next heartbeat gets
// a 401 and the app wipes its own cloud credentials. That path needs no
// cooperation from the device, which is the point — a tablet that is lost,
// broken or permanently offline can still be retired from the console.
//
// Rows are deleted in dependency order so a partial failure cannot leave
// orphans pointing at a device that no longer exists.
func (s *Store) DeleteDevice(deviceID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM commands WHERE device_id=?`,
		`DELETE FROM apk_updates WHERE device_id=?`,
		`DELETE FROM agent_updates WHERE device_id=?`,
		`DELETE FROM devices WHERE id=?`,
	} {
		if _, err := tx.Exec(q, deviceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetDeviceName updates the operator-facing label for a device. Unlike
// SetDeviceGroup this does not clear last_applied_hash: the label is per-device
// and rides down on the heartbeat, so it needs no group config resync.
func (s *Store) SetDeviceName(deviceID, name string) error {
	_, err := s.db.Exec(`UPDATE devices SET name=? WHERE id=?`, name, deviceID)
	return err
}

// ── Commands ─────────────────────────────────────────────────────────────────

func (s *Store) EnqueueCommand(c *Command) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO commands(id,device_id,type,params,status,result,err_msg,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DeviceID, c.Type, c.Params, c.Status, c.Result, c.ErrMsg, c.CreatedAt, c.ExpiresAt)
	return err
}

// PendingCommands returns unsent commands for a device and marks them 'sent'.
func (s *Store) ClaimPendingCommands(deviceID string) ([]Command, error) {
	rows, err := s.db.Query(`SELECT id,device_id,type,params,status,result,err_msg,created_at,expires_at FROM commands WHERE device_id=? AND status='pending' ORDER BY created_at`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		var c Command
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Type, &c.Params, &c.Status, &c.Result, &c.ErrMsg, &c.CreatedAt, &c.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	for _, c := range out {
		s.db.Exec(`UPDATE commands SET status='sent' WHERE id=?`, c.ID)
	}
	return out, nil
}

func (s *Store) ReportCommandResult(id, status, result, errMsg string) error {
	_, err := s.db.Exec(`UPDATE commands SET status=?, result=?, err_msg=? WHERE id=?`, status, result, errMsg, id)
	return err
}

func (s *Store) ListCommands(deviceID string) ([]Command, error) {
	rows, err := s.db.Query(`SELECT id,device_id,type,params,status,result,err_msg,created_at,expires_at FROM commands WHERE device_id=? ORDER BY created_at DESC LIMIT 100`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		var c Command
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Type, &c.Params, &c.Status, &c.Result, &c.ErrMsg, &c.CreatedAt, &c.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ── APK updates ──────────────────────────────────────────────────────────────

func (s *Store) EnqueueAPKUpdate(u *APKUpdate) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO apk_updates(command_id,device_id,package_name,version_name,apk_path,status) VALUES(?,?,?,?,?,?)`,
		u.CommandID, u.DeviceID, u.PackageName, u.VersionName, u.APKPath, u.Status)
	return err
}

func (s *Store) ClaimPendingAPKUpdates(deviceID string) ([]APKUpdate, error) {
	rows, err := s.db.Query(`SELECT command_id,device_id,package_name,version_name,apk_path,status FROM apk_updates WHERE device_id=? AND status='pending'`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APKUpdate
	for rows.Next() {
		var u APKUpdate
		if err := rows.Scan(&u.CommandID, &u.DeviceID, &u.PackageName, &u.VersionName, &u.APKPath, &u.Status); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	for _, u := range out {
		s.db.Exec(`UPDATE apk_updates SET status='sent' WHERE command_id=?`, u.CommandID)
	}
	return out, nil
}

// ── Operators ────────────────────────────────────────────────────────────────

const operatorCols = `email,name,role,password_hash,created_at`

// UpsertOperator writes an account, replacing any existing row with that email.
// Used by the bootstrap CLI, which is expected to be able to reset the admin.
func (s *Store) UpsertOperator(o *Operator) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO operators(`+operatorCols+`) VALUES(?,?,?,?,?)`,
		o.Email, o.Name, o.Role, o.PasswordHash, o.CreatedAt)
	return err
}

// CreateOperator inserts a new account and fails if the email is taken.
func (s *Store) CreateOperator(o *Operator) error {
	_, err := s.db.Exec(`INSERT INTO operators(`+operatorCols+`) VALUES(?,?,?,?,?)`,
		o.Email, o.Name, o.Role, o.PasswordHash, o.CreatedAt)
	return err
}

// GetOperator looks an account up case-insensitively — emails predating
// normalisation may be stored with mixed case.
func (s *Store) GetOperator(email string) (*Operator, error) {
	row := s.db.QueryRow(`SELECT `+operatorCols+` FROM operators WHERE lower(email)=lower(?)`, email)
	return scanOperator(row)
}

func scanOperator(row *sql.Row) (*Operator, error) {
	var o Operator
	if err := row.Scan(&o.Email, &o.Name, &o.Role, &o.PasswordHash, &o.CreatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) ListOperators() ([]Operator, error) {
	rows, err := s.db.Query(`SELECT ` + operatorCols + ` FROM operators ORDER BY lower(email)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operator{}
	for rows.Next() {
		var o Operator
		if err := rows.Scan(&o.Email, &o.Name, &o.Role, &o.PasswordHash, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateOperator changes the profile fields of the account currently at
// currentEmail (which may itself be moved to o.Email).
func (s *Store) UpdateOperator(currentEmail string, o *Operator) error {
	_, err := s.db.Exec(`UPDATE operators SET email=?, name=?, role=? WHERE lower(email)=lower(?)`,
		o.Email, o.Name, o.Role, currentEmail)
	return err
}

func (s *Store) UpdateOperatorPassword(email, passwordHash string) error {
	_, err := s.db.Exec(`UPDATE operators SET password_hash=? WHERE lower(email)=lower(?)`, passwordHash, email)
	return err
}

func (s *Store) DeleteOperator(email string) error {
	_, err := s.db.Exec(`DELETE FROM operators WHERE lower(email)=lower(?)`, email)
	return err
}

// CountAdminsExcept counts admin accounts other than the given email, so callers
// can refuse the change that would leave the console with no administrator.
func (s *Store) CountAdminsExcept(email string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM operators WHERE role=? AND lower(email)<>lower(?)`,
		RoleAdmin, email).Scan(&n)
	return n, err
}

// CountPendingCommands returns how many unsent commands a device has (for the heartbeat).
func (s *Store) CountPendingCommands(deviceID string) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM commands WHERE device_id=? AND status='pending'`, deviceID).Scan(&n)
	return n
}

// CountPendingAPKUpdates returns how many pending APK installs a device has.
// The heartbeat folds this into pending_commands so the app wakes and polls the
// /updates/ channel (where install_apk is actually delivered). Without this, an
// APK install queued while the device is idle would not trigger a poll.
func (s *Store) CountPendingAPKUpdates(deviceID string) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM apk_updates WHERE device_id=? AND status='pending'`, deviceID).Scan(&n)
	return n
}

// APK is a stored application package.
type APK struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Path   string `json:"-"`
}

// DeleteAPK drops the catalogue row for an APK. The file itself is removed
// separately via the apk store; this only forgets the record.
func (s *Store) DeleteAPK(name string) error {
	_, err := s.db.Exec(`DELETE FROM apks WHERE name=?`, name)
	return err
}

// SaveAPK records a stored APK.
func (s *Store) SaveAPK(a *APK) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO apks(name,sha256,size,path) VALUES(?,?,?,?)`, a.Name, a.SHA256, a.Size, a.Path)
	return err
}

func (s *Store) ListAPKs() ([]APK, error) {
	rows, err := s.db.Query(`SELECT name,sha256,size,path FROM apks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APK
	for rows.Next() {
		var a APK
		if err := rows.Scan(&a.Name, &a.SHA256, &a.Size, &a.Path); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ── Agent (self) updates ─────────────────────────────────────────────────────

// Agent rollout states. A device moves queued -> installing -> success|failed.
// "installing" is written by the device immediately before it commits the
// install, so if the process is killed mid-replace the row still records that
// an attempt was in flight and the device can reconcile on next launch.
const (
	AgentQueued     = "queued"
	AgentInstalling = "installing"
	AgentSuccess    = "success"
	AgentFailed     = "failed"
)

// AgentRelease is the current Ali MDM build available to devices.
type AgentRelease struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	FileName    string `json:"file_name"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	CreatedAt   string `json:"created_at"`
}

// AgentUpdate is one device's rollout state for the current release.
type AgentUpdate struct {
	DeviceID          string `json:"device_id"`
	TargetVersionCode int    `json:"target_version_code"`
	Status            string `json:"status"`
	Attempts          int    `json:"attempts"`
	LastError         string `json:"last_error"`
	UpdatedAt         string `json:"updated_at"`
}

// SaveAgentRelease replaces the current release. Rolling out a new build always
// supersedes the previous one, so this is an upsert on the single row.
func (s *Store) SaveAgentRelease(a *AgentRelease) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO agent_release(id,version_code,version_name,file_name,sha256,size,created_at)
		 VALUES(1,?,?,?,?,?,?)`,
		a.VersionCode, a.VersionName, a.FileName, a.SHA256, a.Size, nowISO())
	return err
}

func (s *Store) GetAgentRelease() (*AgentRelease, error) {
	var a AgentRelease
	err := s.db.QueryRow(
		`SELECT version_code,version_name,file_name,sha256,size,created_at FROM agent_release WHERE id=1`).
		Scan(&a.VersionCode, &a.VersionName, &a.FileName, &a.SHA256, &a.Size, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// QueueAgentUpdate (re)arms a rollout for one device, resetting attempts so an
// operator retry after a failure starts from a clean slate.
func (s *Store) QueueAgentUpdate(deviceID string, versionCode int) error {
	_, err := s.db.Exec(
		`INSERT INTO agent_updates(device_id,target_version_code,status,attempts,last_error,updated_at)
		 VALUES(?,?,?,0,'',?)
		 ON CONFLICT(device_id) DO UPDATE SET
		   target_version_code=excluded.target_version_code,
		   status=excluded.status, attempts=0, last_error='', updated_at=excluded.updated_at`,
		deviceID, versionCode, AgentQueued, nowISO())
	return err
}

func (s *Store) GetAgentUpdate(deviceID string) (*AgentUpdate, error) {
	var u AgentUpdate
	err := s.db.QueryRow(
		`SELECT device_id,target_version_code,status,attempts,last_error,updated_at
		 FROM agent_updates WHERE device_id=?`, deviceID).
		Scan(&u.DeviceID, &u.TargetVersionCode, &u.Status, &u.Attempts, &u.LastError, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SetAgentUpdateStatus records progress reported by the device. Attempts are
// incremented server-side on each "installing" report rather than trusted from
// the device, so a tablet stuck in a reboot loop cannot hide its retry count.
// Success is terminal and cannot be downgraded. A device that has already
// confirmed the new build may still be handed a stale offer computed before its
// report landed; without this guard, acting on that offer would overwrite a real
// success with a spurious failure. Re-arming a rollout goes through
// QueueAgentUpdate, which resets the row deliberately.
func (s *Store) SetAgentUpdateStatus(deviceID, status, lastErr string) error {
	inc := 0
	if status == AgentInstalling {
		inc = 1
	}
	_, err := s.db.Exec(
		`UPDATE agent_updates SET status=?, last_error=?, attempts=attempts+?, updated_at=?
		 WHERE device_id=? AND status<>?`, status, lastErr, inc, nowISO(), deviceID, AgentSuccess)
	return err
}

func (s *Store) ListAgentUpdates() ([]AgentUpdate, error) {
	rows, err := s.db.Query(
		`SELECT device_id,target_version_code,status,attempts,last_error,updated_at
		 FROM agent_updates ORDER BY device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentUpdate
	for rows.Next() {
		var u AgentUpdate
		if err := rows.Scan(&u.DeviceID, &u.TargetVersionCode, &u.Status, &u.Attempts, &u.LastError, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// ── Settings ─────────────────────────────────────────────────────────────────

// Keys for server-wide settings. Kept as constants so a typo cannot silently
// read a setting that is never written.
const (
	SettingAlertWebhookURL     = "alert_webhook_url"
	SettingAlertOfflineMinutes = "alert_offline_minutes"
)

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// SetSettingIfAbsent seeds a value without overwriting an operator's choice.
// Used to carry an existing env-var configuration into the database once, so
// upgrading does not silently turn alerting off.
func (s *Store) SetSettingIfAbsent(key, value string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO settings(key,value) VALUES(?,?)`, key, value)
	return err
}

// ── Offline alerting ─────────────────────────────────────────────────────────

const (
	AlertOK      = "ok"
	AlertOffline = "offline"
)

// AlertStates returns the current alert state per device id. Devices with no
// row have never alerted and are treated as "ok".
func (s *Store) AlertStates() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT device_id, state FROM device_alerts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, nil
}

func (s *Store) SetAlertState(deviceID, state string) error {
	_, err := s.db.Exec(
		`INSERT INTO device_alerts(device_id, state, notified_at) VALUES(?,?,?)
		 ON CONFLICT(device_id) DO UPDATE SET state=excluded.state, notified_at=excluded.notified_at`,
		deviceID, state, nowISO())
	return err
}

// nowISO is a helper for timestamps.
func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

// MarshalJSON helper for embedding params.
func MustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ── Event log ────────────────────────────────────────────────────────────────
//
// A record of what happened to the fleet and who did it. The console's bell
// reads this, and because it is server-side it shows an operator what someone
// else changed, and what the server itself noticed while nobody was looking.
//
// Retention is capped: this is a notification feed, not an audit archive, and
// an unbounded table on a small server would be a slow leak.

const (
	EventInfo  = "info"
	EventWarn  = "warn"
	EventError = "error"

	// maxEvents is how many are kept. At this fleet's volume that is months.
	maxEvents = 500
)

type Event struct {
	ID       int64  `json:"id"`
	At       string `json:"at"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Actor    string `json:"actor"`
	DeviceID string `json:"device_id"`
	Summary  string `json:"summary"`
}

// RecordEvent appends one entry and trims the tail. Callers treat this as
// best-effort: failing to log must never fail the action being logged.
func (s *Store) RecordEvent(e *Event) error {
	if e.Severity == "" {
		e.Severity = EventInfo
	}
	res, err := s.db.Exec(
		`INSERT INTO events(at, kind, severity, actor, device_id, summary) VALUES(?,?,?,?,?,?)`,
		nowISO(), e.Kind, e.Severity, e.Actor, e.DeviceID, e.Summary)
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil {
		e.ID = id
		// Trim well past the cap rather than on every insert, so the common
		// path is a single INSERT.
		if id%50 == 0 {
			_, _ = s.db.Exec(`DELETE FROM events WHERE id <= ?`, id-maxEvents)
		}
	}
	return nil
}

// ListEvents returns the most recent entries, newest first.
func (s *Store) ListEvents(limit int) ([]Event, error) {
	if limit <= 0 || limit > maxEvents {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, at, kind, severity, actor, device_id, summary
		 FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.Severity, &e.Actor, &e.DeviceID, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEventsAfter reports how many entries are newer than the given id, which
// is what the console's unread badge shows.
func (s *Store) CountEventsAfter(id int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE id > ?`, id).Scan(&n)
	return n, err
}

// LatestEventID is the marker an operator's "seen everything" points at.
func (s *Store) LatestEventID() (int64, error) {
	var id sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM events`).Scan(&id); err != nil {
		return 0, err
	}
	return id.Int64, nil
}

// Per-operator read marker. Keyed by email so two operators do not clear each
// other's badge.
func eventReadKey(email string) string { return "events_read:" + strings.ToLower(email) }

func (s *Store) EventsReadMarker(email string) int64 {
	v, err := s.GetSetting(eventReadKey(email))
	if err != nil || v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (s *Store) SetEventsReadMarker(email string, id int64) error {
	return s.SetSetting(eventReadKey(email), strconv.FormatInt(id, 10))
}

// DeviceEventStates is the event log's own view of which devices are silent.
func (s *Store) DeviceEventStates() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT device_id, state FROM device_event_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

func (s *Store) SetDeviceEventState(deviceID, state string) error {
	_, err := s.db.Exec(
		`INSERT INTO device_event_state(device_id, state) VALUES(?,?)
		 ON CONFLICT(device_id) DO UPDATE SET state=excluded.state`, deviceID, state)
	return err
}

// ── Console → device file inbox ──────────────────────────────────────────────
//
// A file uploaded once in the console and pushed to a whole group, so sending a
// PDF to twelve tablets is one action rather than twelve. Delivery is tracked
// per device: a push that silently missed half the fleet would be worse than no
// push at all, because nobody would go and check.

// Delivery states. A row goes pending → sent → done, or → failed with a reason.
const (
	FileDeliveryPending = "pending"
	FileDeliverySent    = "sent"
	FileDeliveryDone    = "done"
	FileDeliveryFailed  = "failed"

	// maxFileDeliveryAttempts stops a file the device cannot store — wrong type,
	// no space — from being retried on every heartbeat forever.
	maxFileDeliveryAttempts = 3
)

type File struct {
	Name        string `json:"name"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Path        string `json:"-"`
	UploadedAt  string `json:"uploaded_at"`
}

type FileDelivery struct {
	ID        string `json:"id"`
	DeviceID  string `json:"device_id"`
	FileName  string `json:"file_name"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
	UpdatedAt string `json:"updated_at"`
}

// shortID keeps delivery ids deterministic per (device, file), so re-pushing
// the same file updates the existing row instead of piling up duplicates.
func shortID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func (s *Store) SaveFile(f *File) error {
	// Stamp the struct too, so the caller returns the same timestamp it stored
	// rather than an empty string the list view would later contradict.
	f.UploadedAt = nowISO()
	_, err := s.db.Exec(
		`INSERT INTO files(name,sha256,size,content_type,path,uploaded_at) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(name) DO UPDATE SET sha256=excluded.sha256, size=excluded.size,
		   content_type=excluded.content_type, path=excluded.path, uploaded_at=excluded.uploaded_at`,
		f.Name, f.SHA256, f.Size, f.ContentType, f.Path, f.UploadedAt)
	return err
}

func (s *Store) ListFiles() ([]File, error) {
	rows, err := s.db.Query(`SELECT name,sha256,size,content_type,path,uploaded_at FROM files ORDER BY uploaded_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Name, &f.SHA256, &f.Size, &f.ContentType, &f.Path, &f.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFile(name string) (*File, error) {
	var f File
	err := s.db.QueryRow(
		`SELECT name,sha256,size,content_type,path,uploaded_at FROM files WHERE name=?`, name).
		Scan(&f.Name, &f.SHA256, &f.Size, &f.ContentType, &f.Path, &f.UploadedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// DeleteFile drops the catalogue row and every delivery for it. Files already
// on a tablet stay there: the inbox is the pupil's folder, not a mirror.
func (s *Store) DeleteFile(name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM file_deliveries WHERE file_name=?`, name); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE name=?`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// QueueFileDelivery targets one device. Re-pushing a file to a device that
// already has it resets the row, which is what an operator means by "send it
// again" after a failure.
func (s *Store) QueueFileDelivery(deviceID, fileName string) error {
	_, err := s.db.Exec(
		`INSERT INTO file_deliveries(id,device_id,file_name,status,attempts,last_error,updated_at)
		 VALUES(?,?,?,?,0,'',?)
		 ON CONFLICT(device_id, file_name) DO UPDATE SET
		   status=excluded.status, attempts=0, last_error='', updated_at=excluded.updated_at`,
		"fd-"+shortID(deviceID+fileName), deviceID, fileName, FileDeliveryPending, nowISO())
	return err
}

// ClaimPendingFileDeliveries hands the device its outstanding files and marks
// them sent, so a slow download is not handed out again on the next heartbeat.
func (s *Store) ClaimPendingFileDeliveries(deviceID string) ([]FileDelivery, error) {
	rows, err := s.db.Query(
		`SELECT id,device_id,file_name,status,attempts,last_error,updated_at
		 FROM file_deliveries WHERE device_id=? AND status=? AND attempts < ?`,
		deviceID, FileDeliveryPending, maxFileDeliveryAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileDelivery{}
	for rows.Next() {
		var d FileDelivery
		if err := rows.Scan(&d.ID, &d.DeviceID, &d.FileName, &d.Status, &d.Attempts, &d.LastError, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, d := range out {
		_, _ = s.db.Exec(
			`UPDATE file_deliveries SET status=?, attempts=attempts+1, updated_at=? WHERE id=?`,
			FileDeliverySent, nowISO(), d.ID)
	}
	return out, nil
}

// SetFileDeliveryStatus records what the device did with a file. Success is
// terminal: a device that reports done must not be walked back by a late retry.
func (s *Store) SetFileDeliveryStatus(deviceID, fileName, status, errText string) error {
	_, err := s.db.Exec(
		`UPDATE file_deliveries SET status=?, last_error=?, updated_at=?
		 WHERE device_id=? AND file_name=? AND status<>?`,
		status, errText, nowISO(), deviceID, fileName, FileDeliveryDone)
	return err
}

// CountPendingFileDeliveries drives the heartbeat's "go and fetch" hint.
func (s *Store) CountPendingFileDeliveries(deviceID string) int {
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM file_deliveries WHERE device_id=? AND status=? AND attempts < ?`,
		deviceID, FileDeliveryPending, maxFileDeliveryAttempts).Scan(&n)
	return n
}

// FileDeliveries reports where a push got to, per device.
func (s *Store) FileDeliveries(fileName string) ([]FileDelivery, error) {
	rows, err := s.db.Query(
		`SELECT id,device_id,file_name,status,attempts,last_error,updated_at
		 FROM file_deliveries WHERE file_name=? ORDER BY device_id`, fileName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileDelivery{}
	for rows.Next() {
		var d FileDelivery
		if err := rows.Scan(&d.ID, &d.DeviceID, &d.FileName, &d.Status, &d.Attempts, &d.LastError, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
