package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

// Device mirrors a Ali MDM-enrolled tablet.
type Device struct {
	ID         string
	Name       string
	GroupID    string
	APIKeyHash string
	// LastAppliedHash is the config hash the device last confirmed it applied.
	// The group's current hash differs from this => we must push config again.
	LastAppliedHash string
	// ConfigVersion is vestigial: written once at enrolment and never updated,
	// so it is 0 on every device that has ever run. A device's policy version is
	// its group's — read it from there. Kept only because the column exists.
	ConfigVersion int
	Battery       int
	Wifi          int
	// Charging is only meaningful while the device is checking in: a tablet
	// that went offline on charge keeps the last value it sent.
	Charging bool
	AndroidVer    string
	Model         string
	// The Ali MDM build actually running on the tablet, reported on every
	// heartbeat. Without it the console can only show what a device was *told*
	// to install — which is how a tablet sat on an old build for half an hour
	// with its rollout recorded as successful.
	AppVersionCode int
	AppVersionName string
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
	// PasswordChangedAt is a unix second. Session tokens issued before it are
	// refused, so changing a password — or an admin resetting one — ends every
	// session it opened instead of leaving a stolen token valid for its full
	// twelve hours. Zero means "never changed", which accepts any token.
	PasswordChangedAt int64 `json:"-"`
}

func Open(path string) (*Store, error) {
	// WAL lets readers run while a write is in flight, which is most of what a
	// console polling every few seconds does. busy_timeout makes a concurrent
	// writer wait rather than fail outright.
	//
	// synchronous=NORMAL is the pairing WAL is designed for: commits stop
	// waiting on an fsync every time, and the durability given up is a handful
	// of the most recent transactions if the machine loses power — not
	// corruption, which WAL still rules out. For heartbeats arriving every 30
	// seconds from every tablet, that is the right trade.
	//
	// The cache and temp settings are small absolute numbers on a server with
	// gigabytes: 16MB of page cache holds this whole database, and sorting in
	// memory rather than in a temp file matters for the event feed's ORDER BY.
	db, err := sql.Open("sqlite", path+
		"?_pragma=journal_mode(WAL)"+
		"&_pragma=busy_timeout(5000)"+
		"&_pragma=synchronous(NORMAL)"+
		"&_pragma=cache_size(-16000)"+
		"&_pragma=temp_store(MEMORY)")
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
		app_version_code INTEGER NOT NULL DEFAULT 0,
		app_version_name TEXT NOT NULL DEFAULT '',
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
		path TEXT NOT NULL,
		-- Read out of the APK's own manifest at upload. The file name is
		-- whatever the operator's download happened to be called, and typing the
		-- package by hand is a step that can be got wrong in a way nothing
		-- catches until an install silently targets the wrong app.
		package_name TEXT NOT NULL DEFAULT '',
		version_name TEXT NOT NULL DEFAULT ''
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
		-- Where the file sits inside the inbox, e.g. "Juz30/Surah-078". Empty
		-- means the top of the inbox. Storage on the server stays flat; this is
		-- what the tablet rebuilds the folders from.
		rel_path TEXT NOT NULL DEFAULT '',
		-- The name the device saves it under. The primary key has to carry the
		-- folders to stay unique, and may be disambiguated further, so it is no
		-- longer something a pupil should ever see. Empty on rows written before
		-- this column existed, where the name can still be derived.
		file_name TEXT NOT NULL DEFAULT '',
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
	-- Last-known contents of a device's inbox folder. Cached rather than queried
	-- live: the tablet is behind NAT, so the console asks by command and the
	-- answer arrives later. A stale listing with its timestamp beats none.
	CREATE TABLE IF NOT EXISTS device_inbox(
		device_id TEXT PRIMARY KEY,
		entries TEXT NOT NULL DEFAULT '[]',
		fetched_at TEXT NOT NULL DEFAULT ''
	);
	-- What is actually installed on a tablet, as opposed to what its policy says
	-- should be. The two drift: an app installed before enrollment, one left
	-- behind after being dropped from the whitelist, or an OEM package whose
	-- name differs from the one the policy names. Same shape as device_inbox and
	-- for the same reason — the answer arrives on the device's own schedule.
	CREATE TABLE IF NOT EXISTS device_apps(
		device_id TEXT PRIMARY KEY,
		entries TEXT NOT NULL DEFAULT '[]',
		fetched_at TEXT NOT NULL DEFAULT ''
	);
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
	
	-- Indexes for the queries that run on a schedule rather than on a click.
	-- Without them every one of these is a full table scan, and they are the
	-- ones that repeat: twice per heartbeat per device, and once per file every
	-- time the Files page refreshes.
	--
	-- A heartbeat asks "anything pending for me?" of three tables.
	CREATE INDEX IF NOT EXISTS idx_apk_updates_device
		ON apk_updates(device_id, status);
	CREATE INDEX IF NOT EXISTS idx_file_deliveries_device
		ON file_deliveries(device_id, status);
	CREATE INDEX IF NOT EXISTS idx_agent_updates_device
		ON agent_updates(device_id);
	-- The Files page asks "where did this one get to?" per file.
	CREATE INDEX IF NOT EXISTS idx_file_deliveries_file
		ON file_deliveries(file_name);
	-- A device's own history, and the count beside its name.
	CREATE INDEX IF NOT EXISTS idx_events_device
		ON events(device_id, id DESC);
	-- Group pages count their devices; moving a group rewrites them.
	CREATE INDEX IF NOT EXISTS idx_devices_group
		ON devices(group_id);
	-- Every sign-in and every user edit matches on lower(email), which cannot
	-- use an ordinary index on email.
	CREATE UNIQUE INDEX IF NOT EXISTS idx_operators_email_lower
		ON operators(lower(email));
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
		{"password_changed_at", `ALTER TABLE operators ADD COLUMN password_changed_at INTEGER NOT NULL DEFAULT 0`},
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

	// Devices predate reporting their own build; add the columns in place so an
	// existing fleet keeps its rows across the upgrade.
	for _, c := range []struct{ name, ddl string }{
		{"app_version_code", `ALTER TABLE devices ADD COLUMN app_version_code INTEGER NOT NULL DEFAULT 0`},
		{"app_version_name", `ALTER TABLE devices ADD COLUMN app_version_name TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(db, "devices", c.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(c.ddl); err != nil {
				return err
			}
		}
	}

	// Files uploaded before folder upload existed have no folder to sit in, and
	// an empty rel_path means exactly that: put it at the top of the inbox.
	// Packages uploaded before the manifest was read at upload have no package
	// name recorded. It is filled in the first time they are listed.
	for _, c := range []struct{ name, ddl string }{
		{"package_name", `ALTER TABLE apks ADD COLUMN package_name TEXT NOT NULL DEFAULT ''`},
		{"version_name", `ALTER TABLE apks ADD COLUMN version_name TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(db, "apks", c.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(c.ddl); err != nil {
				return err
			}
		}
	}
	// apk_updates had nowhere to put a reason, so a failed install recorded that
	// it failed and nothing about why — which is the one moment an operator
	// needs the text the device sent.
	// The device has always sent whether it is on charge and the server has
	// always parsed it, then dropped it on the floor. 43% falling and 43%
	// climbing are different situations for someone deciding whether to go and
	// plug a trolley in.
	for _, c := range []struct{ name, ddl string }{
		{"charging", `ALTER TABLE devices ADD COLUMN charging INTEGER NOT NULL DEFAULT 0`},
	} {
		has, err := hasColumn(db, "devices", c.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(c.ddl); err != nil {
				return err
			}
		}
	}
	for _, c := range []struct{ name, ddl string }{
		{"last_error", `ALTER TABLE apk_updates ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(db, "apk_updates", c.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(c.ddl); err != nil {
				return err
			}
		}
	}
	for _, c := range []struct{ name, ddl string }{
		{"rel_path", `ALTER TABLE files ADD COLUMN rel_path TEXT NOT NULL DEFAULT ''`},
		// Left empty for existing rows on purpose: their names are still
		// derivable from the key, because before this column existed a name
		// that needed disambiguating simply overwrote the file it collided with.
		{"file_name", `ALTER TABLE files ADD COLUMN file_name TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(db, "files", c.name)
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
		`INSERT OR REPLACE INTO devices(id,name,group_id,api_key_hash,last_applied_hash,config_version,battery,wifi,charging,android_ver,model,app_version_code,app_version_name,last_seen,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.GroupID, d.APIKeyHash, d.LastAppliedHash, d.ConfigVersion,
		d.Battery, d.Wifi, d.Charging, d.AndroidVer, d.Model, d.AppVersionCode, d.AppVersionName, d.LastSeen, d.CreatedAt)
	return err
}

func (s *Store) GetDevice(id string) (*Device, error) {
	row := s.db.QueryRow(`SELECT id,name,group_id,api_key_hash,last_applied_hash,config_version,battery,wifi,charging,android_ver,model,app_version_code,app_version_name,last_seen,created_at FROM devices WHERE id=?`, id)
	var d Device
	if err := row.Scan(&d.ID, &d.Name, &d.GroupID, &d.APIKeyHash, &d.LastAppliedHash, &d.ConfigVersion, &d.Battery, &d.Wifi, &d.Charging, &d.AndroidVer, &d.Model, &d.AppVersionCode, &d.AppVersionName, &d.LastSeen, &d.CreatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id,name,group_id,api_key_hash,last_applied_hash,config_version,battery,wifi,charging,android_ver,model,app_version_code,app_version_name,last_seen,created_at FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.GroupID, &d.APIKeyHash, &d.LastAppliedHash, &d.ConfigVersion, &d.Battery, &d.Wifi, &d.Charging, &d.AndroidVer, &d.Model, &d.AppVersionCode, &d.AppVersionName, &d.LastSeen, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// UpdateHeartbeat records telemetry + the config hash the device just applied.
func (s *Store) UpdateHeartbeat(id, appliedHash string, battery, wifi int, charging bool, androidVer, model, lastSeen string) error {
	_, err := s.db.Exec(`UPDATE devices SET last_applied_hash=?, battery=?, wifi=?, charging=?, android_ver=?, model=?, last_seen=? WHERE id=?`,
		appliedHash, battery, wifi, charging, androidVer, model, lastSeen, id)
	return err
}

// SetDeviceAppVersion records the build a tablet says it is running, straight
// from its heartbeat. Every build in the fleet reports one, so there is no
// missing-version case to carry.
func (s *Store) SetDeviceAppVersion(id string, code int, name string) error {
	_, err := s.db.Exec(
		`UPDATE devices SET app_version_code=?, app_version_name=? WHERE id=?`, code, name, id)
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

// ResendConfig makes a device look out of date so its next check-in is answered
// with the whole policy again.
//
// Config is only sent when the group's hash differs from the one the device was
// last handed, so a tablet whose settings have drifted — someone changed
// something on the device, a write failed halfway, the app was reinstalled —
// sits there looking perfectly in sync and is never corrected. Clearing the
// hash is the only lever: the tablets are behind NAT, so nothing can be pushed
// to them, and this is what "re-apply the policy" means on a pull channel.
func (s *Store) ResendConfig(deviceID string) error {
	_, err := s.db.Exec(`UPDATE devices SET last_applied_hash='' WHERE id=?`, deviceID)
	return err
}

// ResendConfigToGroup does the same for every device in a group, and says how
// many that was.
func (s *Store) ResendConfigToGroup(groupID string) (int, error) {
	res, err := s.db.Exec(`UPDATE devices SET last_applied_hash='' WHERE group_id=?`, groupID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
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
	// Every per-device table, or a tablet that re-enrols on the same id inherits
	// the old one's alert state, delivery history and inbox listing.
	for _, q := range []string{
		`DELETE FROM commands WHERE device_id=?`,
		`DELETE FROM apk_updates WHERE device_id=?`,
		`DELETE FROM agent_updates WHERE device_id=?`,
		`DELETE FROM device_alerts WHERE device_id=?`,
		`DELETE FROM device_event_state WHERE device_id=?`,
		`DELETE FROM file_deliveries WHERE device_id=?`,
		`DELETE FROM device_inbox WHERE device_id=?`,
		`DELETE FROM device_apps WHERE device_id=?`,
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

const (
	// maxCommandsPerDevice is how much of a device's command history is kept.
	// The console shows the last 100; the rest is only ever weight. Without a
	// cap these rows lived until the device was unenrolled, which for a tablet
	// in daily service means for ever — a screenshot, a lock, a log request and
	// a config resend all leave one behind.
	maxCommandsPerDevice = 200
	// maxAPKUpdatesPerDevice is the same idea for install records.
	maxAPKUpdatesPerDevice = 100
	// pruneEvery spaces the tidying out so the common path stays one INSERT.
	pruneEvery = 50
)

// writes counts inserts across both history tables, so pruning happens on a
// steady cadence rather than on every call.
var writes atomic.Uint64

func (s *Store) EnqueueCommand(c *Command) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO commands(id,device_id,type,params,status,result,err_msg,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DeviceID, c.Type, c.Params, c.Status, c.Result, c.ErrMsg, c.CreatedAt, c.ExpiresAt)
	if err == nil && writes.Add(1)%pruneEvery == 0 {
		s.pruneDeviceHistory(c.DeviceID)
	}
	return err
}

// pruneDeviceHistory drops the oldest finished records for one device.
//
// Finished only: a pending or sent command is work the device has not done yet,
// and deleting it would silently cancel it. Everything still outstanding stays
// however old it is, which is also the honest thing — a command that has sat
// unsent for a week is a fact worth being able to see.
func (s *Store) pruneDeviceHistory(deviceID string) {
	if deviceID == "" {
		return
	}
	_, _ = s.db.Exec(
		`DELETE FROM commands
		  WHERE device_id = ?
		    AND status NOT IN ('pending','sent')
		    AND rowid NOT IN (
		      SELECT rowid FROM commands WHERE device_id = ?
		       ORDER BY created_at DESC LIMIT ?
		    )`,
		deviceID, deviceID, maxCommandsPerDevice)
	// apk_updates has no timestamp of its own; rowid is insertion order, which
	// is the same thing for a table nothing ever rewrites in place.
	_, _ = s.db.Exec(
		`DELETE FROM apk_updates
		  WHERE device_id = ?
		    AND status NOT IN ('pending','sent')
		    AND rowid NOT IN (
		      SELECT rowid FROM apk_updates WHERE device_id = ?
		       ORDER BY rowid DESC LIMIT ?
		    )`,
		deviceID, deviceID, maxAPKUpdatesPerDevice)
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

// ReportCommandResult records what a device did with one of its own commands.
//
// Scoped by device on purpose. The command id is the only thing identifying the
// row, and it is handed to the device that must run it — but nothing stopped a
// second device presenting a valid key of its own and writing a result for a
// command that was never theirs. The WHERE clause makes that a no-op, and the
// caller turns a no-op into 404 rather than a silent success.
func (s *Store) ReportCommandResult(id, deviceID, reportStatus, result, errMsg string) error {
	res, err := s.db.Exec(
		`UPDATE commands SET status=?, result=?, err_msg=? WHERE id=? AND device_id=?`,
		reportStatus, result, errMsg, id, deviceID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	if n > 0 {
		return nil
	}

	// An app install is not a row in commands. It lives in apk_updates with a
	// command_id of its own, and the device reports its outcome down the same
	// endpoint — so an id that matches nothing here may still be a legitimate
	// install report. Until now that report updated no row at all and was
	// answered 200 regardless, which is why an install's outcome was never
	// recorded anywhere: rows went pending, then sent, then sat at sent for
	// ever. Record it.
	status := "installed"
	if strings.EqualFold(reportStatus, "error") || strings.EqualFold(reportStatus, "failed") {
		status = "failed"
	}
	// Read the package before writing, so the caller can name it in the feed.
	var pkg string
	_ = s.db.QueryRow(`SELECT package_name FROM apk_updates WHERE command_id=? AND device_id=?`,
		id, deviceID).Scan(&pkg)
	res, err = s.db.Exec(
		`UPDATE apk_updates SET status=?, last_error=? WHERE command_id=? AND device_id=?`,
		status, errMsg, id, deviceID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return sql.ErrNoRows
	}
	if status == "failed" {
		return &InstallFailed{Package: pkg, Reason: errMsg}
	}
	return nil
}

// InstallFailed says the report recorded was an app install that did not work.
// Not an error in the "call failed" sense — the write succeeded — but the one
// outcome the caller should say something about, because a failed install was
// previously silent everywhere an operator looks.
type InstallFailed struct {
	Package string
	Reason  string
}

func (e *InstallFailed) Error() string {
	if e.Reason == "" {
		return e.Package + " failed to install"
	}
	return e.Package + " failed to install: " + e.Reason
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
	if err == nil && writes.Add(1)%pruneEvery == 0 {
		s.pruneDeviceHistory(u.DeviceID)
	}
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

const operatorCols = `email,name,role,password_hash,created_at,password_changed_at`

// Writes list their columns separately: operatorCols is what a row reads back
// as, and a column added there must not silently unbalance an INSERT.
const operatorInsertCols = `email,name,role,password_hash,created_at`

// UpsertOperator writes an account, replacing any existing row with that email.
// Used by the bootstrap CLI, which is expected to be able to reset the admin.
func (s *Store) UpsertOperator(o *Operator) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO operators(`+operatorInsertCols+`) VALUES(?,?,?,?,?)`,
		o.Email, o.Name, o.Role, o.PasswordHash, o.CreatedAt)
	return err
}

// CreateOperator inserts a new account and fails if the email is taken.
func (s *Store) CreateOperator(o *Operator) error {
	_, err := s.db.Exec(`INSERT INTO operators(`+operatorInsertCols+`) VALUES(?,?,?,?,?)`,
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
	if err := row.Scan(&o.Email, &o.Name, &o.Role, &o.PasswordHash, &o.CreatedAt, &o.PasswordChangedAt); err != nil {
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
		if err := rows.Scan(&o.Email, &o.Name, &o.Role, &o.PasswordHash, &o.CreatedAt, &o.PasswordChangedAt); err != nil {
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

// UpdateOperatorPassword also stamps the moment, which is what invalidates the
// session tokens the old password opened. A password change that leaves a
// stolen token working for another twelve hours is not a password change.
func (s *Store) UpdateOperatorPassword(email, passwordHash string) error {
	_, err := s.db.Exec(
		`UPDATE operators SET password_hash=?, password_changed_at=? WHERE lower(email)=lower(?)`,
		passwordHash, time.Now().Unix(), email)
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
	// PackageName and VersionName come from the APK's own manifest, so the
	// console can say what an upload actually is rather than only what the file
	// was called. Empty on rows written before they were recorded.
	PackageName string `json:"package_name"`
	VersionName string `json:"version_name"`
}

// DeleteAPK drops the catalogue row for an APK. The file itself is removed
// separately via the apk store; this only forgets the record.
func (s *Store) DeleteAPK(name string) error {
	_, err := s.db.Exec(`DELETE FROM apks WHERE name=?`, name)
	return err
}

// SaveAPK records a stored APK.
func (s *Store) SaveAPK(a *APK) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO apks(name,sha256,size,path,package_name,version_name)
		 VALUES(?,?,?,?,?,?)`,
		a.Name, a.SHA256, a.Size, a.Path, a.PackageName, a.VersionName)
	return err
}

func (s *Store) ListAPKs() ([]APK, error) {
	rows, err := s.db.Query(
		`SELECT name,sha256,size,path,package_name,version_name FROM apks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APK
	for rows.Next() {
		var a APK
		if err := rows.Scan(&a.Name, &a.SHA256, &a.Size, &a.Path,
			&a.PackageName, &a.VersionName); err != nil {
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
	return s.ListEventsPage(0, limit, "", "")
}

// ListEventsPage walks the feed backwards from a cursor.
//
// Paging is by id rather than offset: ids only ever increase, so a page cannot
// skip or repeat an entry because something arrived while the operator was
// reading — which an OFFSET would do on a feed that grows from the top.
//
// beforeID of 0 starts at the newest. severity, when given, narrows to one
// level, which is how an operator finds the failures among the routine traffic.
// deviceID narrows to one device's own history; empty means the whole fleet.
func (s *Store) ListEventsPage(beforeID int64, limit int, severity, deviceID string) ([]Event, error) {
	if limit <= 0 || limit > maxEvents {
		limit = 50
	}
	q := `SELECT id, at, kind, severity, actor, device_id, summary FROM events WHERE 1=1`
	args := []any{}
	if beforeID > 0 {
		q += ` AND id < ?`
		args = append(args, beforeID)
	}
	if deviceID != "" {
		q += ` AND device_id = ?`
		args = append(args, deviceID)
	}
	switch severity {
	case EventWarn:
		// "Needs attention" means warnings and errors together: an operator
		// looking for trouble does not care which label it landed under.
		q += ` AND severity IN (?, ?)`
		args = append(args, EventWarn, EventError)
	case EventError:
		q += ` AND severity = ?`
		args = append(args, EventError)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
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

// CountEvents reports how many entries are held, so the console can say how
// much history there is rather than only how much it has fetched.
// CountEvents is how much history is retained, for the whole fleet or for one
// device, so a page can say what it is showing a slice of.
func (s *Store) CountEvents() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	return n
}

func (s *Store) CountDeviceEvents(deviceID string) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE device_id = ?`, deviceID).Scan(&n)
	return n
}

// MaxEventsKept is the retention cap, surfaced so the console can explain why
// older entries are not there rather than looking like it lost them.
func MaxEventsKept() int { return maxEvents }

// ClearEvents empties the feed and reports how many entries went.
//
// Bounded by the newest id at the moment it runs, rather than an unqualified
// DELETE: an event recorded while the clear is in flight — a tablet going
// quiet, another operator mid-action — is newer than what the admin asked to
// clear, and survives it.
//
// Ids must not be reused across this. Read markers are ids, so an event
// numbered below one would arrive already counted as read. The AUTOINCREMENT
// on events.id is what guarantees that and is the reason it is there; drop it
// and emptying the table sends the next id back to 1.
func (s *Store) ClearEvents() (int64, error) {
	var upTo sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM events`).Scan(&upTo); err != nil {
		return 0, err
	}
	if !upTo.Valid {
		return 0, nil
	}
	res, err := s.db.Exec(`DELETE FROM events WHERE id <= ?`, upTo.Int64)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
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
	// RelPath is the folder the file belongs in on the tablet, without the file
	// name — "Juz30/Surah-078", or empty for the top of the inbox.
	RelPath string `json:"rel_path"`
	// FileName is what the device saves the file as, inside RelPath. Name is the
	// catalogue key and carries the folders, so it is not that. Empty on rows
	// written before this field existed.
	FileName   string `json:"file_name"`
	UploadedAt string `json:"uploaded_at"`
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
		`INSERT INTO files(name,sha256,size,content_type,path,rel_path,file_name,uploaded_at)
		 VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(name) DO UPDATE SET sha256=excluded.sha256, size=excluded.size,
		   content_type=excluded.content_type, path=excluded.path, rel_path=excluded.rel_path,
		   file_name=excluded.file_name, uploaded_at=excluded.uploaded_at`,
		f.Name, f.SHA256, f.Size, f.ContentType, f.Path, f.RelPath, f.FileName, f.UploadedAt)
	return err
}

func (s *Store) ListFiles() ([]File, error) {
	rows, err := s.db.Query(`SELECT name,sha256,size,content_type,path,rel_path,file_name,uploaded_at FROM files ORDER BY uploaded_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Name, &f.SHA256, &f.Size, &f.ContentType, &f.Path, &f.RelPath, &f.FileName, &f.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFile(name string) (*File, error) {
	var f File
	err := s.db.QueryRow(
		`SELECT name,sha256,size,content_type,path,rel_path,file_name,uploaded_at FROM files WHERE name=?`, name).
		Scan(&f.Name, &f.SHA256, &f.Size, &f.ContentType, &f.Path, &f.RelPath, &f.FileName, &f.UploadedAt)
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

// maxFileDeliveryBatch bounds one check-in's worth of downloads.
//
// The device fetches these one after another and only reports as it finishes
// each, so everything it is handed is at risk for as long as the batch takes.
// Sending a folder made that concrete: a whole album handed over at once meant
// one interrupted download stranded every file behind it. A batch drains over
// several check-ins instead, and each check-in is woken immediately by the poke
// that follows a push.
const maxFileDeliveryBatch = 25

// staleSentAfter is how long a claimed file may sit unreported before it is
// offered again. A device that was rebooted mid-download would otherwise leave
// its remaining files marked sent for ever, showing as pending in the console
// and never being retried. Attempts are still capped, so this cannot loop.
const staleSentAfter = 30 * time.Minute

// ClaimPendingFileDeliveries hands the device its next files and marks them
// sent, so a slow download is not handed out again on the next heartbeat.
func (s *Store) ClaimPendingFileDeliveries(deviceID string) ([]FileDelivery, error) {
	rows, err := s.db.Query(
		`SELECT id,device_id,file_name,status,attempts,last_error,updated_at
		 FROM file_deliveries
		 WHERE device_id=? AND attempts < ?
		   AND (status=? OR (status=? AND updated_at < ?))
		 ORDER BY updated_at LIMIT ?`,
		deviceID, maxFileDeliveryAttempts,
		FileDeliveryPending,
		FileDeliverySent, time.Now().UTC().Add(-staleSentAfter).Format(time.RFC3339),
		maxFileDeliveryBatch)
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
//
// It counts exactly what a claim would hand over, stale claimed files included:
// a hint of zero means the device never asks, so anything the claim would
// re-offer but this does not count is a file that is never retried.
func (s *Store) CountPendingFileDeliveries(deviceID string) int {
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM file_deliveries
		 WHERE device_id=? AND attempts < ?
		   AND (status=? OR (status=? AND updated_at < ?))`,
		deviceID, maxFileDeliveryAttempts,
		FileDeliveryPending,
		FileDeliverySent, time.Now().UTC().Add(-staleSentAfter).Format(time.RFC3339)).Scan(&n)
	return n
}

// FileDeliveries reports where a push got to, per device.
// DeliveryCounts is how far one file has got: how many devices it was sent to,
// and where each of them ended up.
type DeliveryCounts struct {
	Targets   int `json:"targets"`
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

// AllFileDeliveryCounts summarises every file in one query.
//
// The Files page used to ask per file, and it refreshes every fifteen seconds
// while it is open: a library of two hundred tracks meant two hundred queries a
// quarter-minute, and each one was a full table scan before file_deliveries was
// indexed. The page needs four numbers per file, not the rows, so it should ask
// for four numbers.
func (s *Store) AllFileDeliveryCounts() (map[string]DeliveryCounts, error) {
	rows, err := s.db.Query(
		`SELECT file_name, status, COUNT(*) FROM file_deliveries GROUP BY file_name, status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DeliveryCounts{}
	for rows.Next() {
		var name, status string
		var n int
		if err := rows.Scan(&name, &status, &n); err != nil {
			return nil, err
		}
		c := out[name]
		c.Targets += n
		switch status {
		case FileDeliveryDone:
			c.Delivered += n
		case FileDeliveryFailed:
			c.Failed += n
		default:
			// Anything not finished counts as still on its way, which is what
			// the console shows and what "sent but unconfirmed" really means.
			c.Pending += n
		}
		out[name] = c
	}
	return out, rows.Err()
}

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

// SetDeviceInbox stores what a tablet reported its inbox folder contains.
func (s *Store) SetDeviceInbox(deviceID, entriesJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO device_inbox(device_id, entries, fetched_at) VALUES(?,?,?)
		 ON CONFLICT(device_id) DO UPDATE SET entries=excluded.entries, fetched_at=excluded.fetched_at`,
		deviceID, entriesJSON, nowISO())
	return err
}

// DeviceInbox returns the cached listing and when it was taken. An empty
// timestamp means the device has never reported.
func (s *Store) DeviceInbox(deviceID string) (entriesJSON, fetchedAt string) {
	entriesJSON, fetchedAt = "[]", ""
	_ = s.db.QueryRow(`SELECT entries, fetched_at FROM device_inbox WHERE device_id=?`, deviceID).
		Scan(&entriesJSON, &fetchedAt)
	return entriesJSON, fetchedAt
}

// SetDeviceApps stores what a tablet reported it has installed.
func (s *Store) SetDeviceApps(deviceID, entriesJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO device_apps(device_id, entries, fetched_at) VALUES(?,?,?)
		 ON CONFLICT(device_id) DO UPDATE SET entries=excluded.entries, fetched_at=excluded.fetched_at`,
		deviceID, entriesJSON, nowISO())
	return err
}

// DeviceApps returns the cached inventory and when it was taken. An empty
// timestamp means the device has never reported.
func (s *Store) DeviceApps(deviceID string) (entriesJSON, fetchedAt string) {
	entriesJSON, fetchedAt = "[]", ""
	_ = s.db.QueryRow(`SELECT entries, fetched_at FROM device_apps WHERE device_id=?`, deviceID).
		Scan(&entriesJSON, &fetchedAt)
	return entriesJSON, fetchedAt
}
