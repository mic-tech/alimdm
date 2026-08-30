package store

import (
	"database/sql"
	"encoding/json"
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

// nowISO is a helper for timestamps.
func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

// MarshalJSON helper for embedding params.
func MustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
