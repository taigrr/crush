package pairing

import (
	"cmp"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Role is a device permission preset.
type Role string

// Device roles.
const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"

	DefaultRole = RoleOperator
)

// Scopes granted by each role (see the spec's Permissions table). Opt-in
// scopes (shell, cluster:approve) are never part of a role.
var roleScopes = map[Role][]string{
	RoleViewer:   {"sessions:read"},
	RoleOperator: {"sessions:read", "sessions:prompt", "permissions:answer", "config:model"},
	RoleAdmin:    {"sessions:read", "sessions:prompt", "permissions:answer", "permissions:always", "config:model", "sessions:move", "devices:approve"},
}

// ParseRole validates a role name; empty means DefaultRole.
func ParseRole(s string) (Role, error) {
	if s == "" {
		return DefaultRole, nil
	}
	r := Role(s)
	if _, ok := roleScopes[r]; !ok {
		return "", fmt.Errorf("unknown role %q (want viewer, operator, or admin)", s)
	}
	return r, nil
}

// Scopes returns the scopes of a role.
func (r Role) Scopes() []string { return slices.Clone(roleScopes[r]) }

// ClientsAccount is the NATS account devices live in.
const ClientsAccount = "CLIENTS"

// NATSPermissions is the subject allow-list of a device NATS user.
type NATSPermissions struct {
	Publish   []string `json:"publish"`
	Subscribe []string `json:"subscribe"`
}

// DevicePermissions returns the scoped NATS permissions for a device. It
// is derived only from the device ID, so a replicated record can never
// widen it.
func DevicePermissions(deviceID string) NATSPermissions {
	own := "crush.client.v1." + deviceID + ".>"
	return NATSPermissions{
		Publish:   []string{own},
		Subscribe: []string{own, "crush.client.v1.broadcast.>", "_INBOX." + deviceID + ".>"},
	}
}

// Device is one replicated device record.
type Device struct {
	DeviceID   string     `json:"device_id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform,omitempty"`
	Model      string     `json:"model,omitempty"`
	AppVersion string     `json:"app_version,omitempty"`
	PublicKey  []byte     `json:"public_key"`
	Role       Role       `json:"role"`
	Scopes     []string   `json:"scopes"`
	PairedAt   time.Time  `json:"paired_at"`
	PairedVia  string     `json:"paired_via"`
	ApprovedBy string     `json:"approved_by"`
	Rev        int        `json:"rev"`
	UpdatedAt  time.Time  `json:"updated_at"`
	UpdatedBy  string     `json:"updated_by"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// Revoked reports whether the device has been revoked.
func (d Device) Revoked() bool { return d.RevokedAt != nil }

// Validate checks that a record is well formed and self-consistent.
func (d Device) Validate() error {
	if len(d.PublicKey) != ed25519.PublicKeySize {
		return errors.New("device record: bad public key")
	}
	if d.DeviceID != DeviceID(d.PublicKey) {
		return errors.New("device record: device ID does not match key")
	}
	if _, ok := roleScopes[d.Role]; !ok {
		return fmt.Errorf("device record: unknown role %q", d.Role)
	}
	if d.Rev < 1 || len(d.Name) > maxTextBytes {
		return errors.New("device record: bad rev or name")
	}
	return nil
}

// newer reports whether a should replace b under the spec merge rule.
func newer(a, b Device) bool {
	if a.Revoked() != b.Revoked() {
		return a.Revoked()
	}
	if a.Rev != b.Rev {
		return a.Rev > b.Rev
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.UpdatedBy < b.UpdatedBy
}

// ErrDeviceNotFound is returned for an unknown device ID.
var ErrDeviceNotFound = errors.New("device not found")

const (
	devicesFile    = "devices.json"
	devicesVersion = 1
)

type registryFile struct {
	Version int      `json:"version"`
	Devices []Device `json:"devices"`
}

// Registry is this node's copy of the replicated device registry,
// persisted to devices.json (0600).
type Registry struct {
	path string

	mu      sync.Mutex
	devices map[string]Device

	watchMu  sync.Mutex
	watchers []func(Device)
}

// OpenRegistry loads (or creates) the registry in dir.
func OpenRegistry(dir string) (*Registry, error) {
	r := &Registry{path: filepath.Join(dir, devicesFile), devices: map[string]Device{}}
	data, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r, nil
	case err != nil:
		return nil, err
	}
	var f registryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.path, err)
	}
	if f.Version != devicesVersion {
		return nil, fmt.Errorf("%s: unsupported version %d", r.path, f.Version)
	}
	for _, d := range f.Devices {
		if d.Validate() == nil {
			r.devices[d.DeviceID] = d
		}
	}
	return r, nil
}

// Watch registers fn to run after every applied change.
func (r *Registry) Watch(fn func(Device)) {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	r.watchers = append(r.watchers, fn)
}

func (r *Registry) notify(d Device) {
	r.watchMu.Lock()
	ws := slices.Clone(r.watchers)
	r.watchMu.Unlock()
	for _, fn := range ws {
		fn(d)
	}
}

// List returns all records sorted by pairing time.
func (r *Registry) List() []Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Device) int {
		return cmp.Or(a.PairedAt.Compare(b.PairedAt), cmp.Compare(a.DeviceID, b.DeviceID))
	})
	return out
}

// Get returns one record.
func (r *Registry) Get(id string) (Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	return d, ok
}

// Merge applies a record (local or replicated) if it wins over the
// current one. It reports whether anything changed.
func (r *Registry) Merge(d Device) (bool, error) {
	if err := d.Validate(); err != nil {
		return false, err
	}
	d.Scopes = slices.Clone(d.Scopes)
	r.mu.Lock()
	cur, ok := r.devices[d.DeviceID]
	if ok && !newer(d, cur) {
		r.mu.Unlock()
		return false, nil
	}
	r.devices[d.DeviceID] = d
	err := r.saveLocked()
	if err != nil {
		if ok {
			r.devices[d.DeviceID] = cur
		} else {
			delete(r.devices, d.DeviceID)
		}
	}
	r.mu.Unlock()
	if err != nil {
		return false, err
	}
	r.notify(d)
	return true, nil
}

// Revoke marks a device revoked by nodeID and returns the new record.
func (r *Registry) Revoke(id, nodeID string, now time.Time) (Device, error) {
	d, ok := r.Get(id)
	if !ok {
		return Device{}, ErrDeviceNotFound
	}
	if d.Revoked() {
		return d, nil
	}
	revoked := now.UTC()
	d.RevokedAt = &revoked
	d.Rev++
	d.UpdatedAt, d.UpdatedBy = revoked, nodeID
	if _, err := r.Merge(d); err != nil {
		return Device{}, err
	}
	return d, nil
}

// Digest summarizes the registry for convergence checks.
func (r *Registry) Digest() string {
	h := sha256.New()
	for _, d := range r.sortedByID() {
		fmt.Fprintf(h, "%s:%d:%t\n", d.DeviceID, d.Rev, d.Revoked())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Registry) sortedByID() []Device {
	out := r.List()
	slices.SortFunc(out, func(a, b Device) int { return cmp.Compare(a.DeviceID, b.DeviceID) })
	return out
}

func (r *Registry) saveLocked() error {
	f := registryFile{Version: devicesVersion, Devices: make([]Device, 0, len(r.devices))}
	for _, d := range r.devices {
		f.Devices = append(f.Devices, d)
	}
	slices.SortFunc(f.Devices, func(a, b Device) int { return cmp.Compare(a.DeviceID, b.DeviceID) })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(r.path, data)
}

func writeFile0600(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".devices-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
