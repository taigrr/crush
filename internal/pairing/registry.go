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
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Role is a device permission preset. Roles are kept only so existing
// records and wire fields stay parseable: every paired device has full
// access (see the spec's Permissions section), whatever its stored role.
type Role string

// Device roles.
const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"

	// DefaultRole is the role written into new records: the one whose
	// scopes, with the opt-in ones, are every scope, so members still
	// running the role code accept and honor the record.
	DefaultRole = RoleAdmin
)

// Scopes each role granted before roles were dropped. They only validate
// stored records now.
var roleScopes = map[Role][]string{
	RoleViewer:   {"sessions:read"},
	RoleOperator: {"sessions:read", "sessions:prompt", "permissions:answer", "config:model"},
	RoleAdmin:    {"sessions:read", "sessions:prompt", "permissions:answer", "permissions:always", "config:model", "sessions:move", "devices:approve"},
}

// optInScopes were granted explicitly on top of a role.
var optInScopes = []string{"shell", "cluster:approve"}

// AllScopes is every device scope. Every paired device holds all of them.
func AllScopes() []string {
	return append(slices.Clone(roleScopes[RoleAdmin]), optInScopes...)
}

// ParseRole accepts a role name for compatibility and always returns
// DefaultRole: roles no longer change what a device may do. Unknown names
// are still refused so typos surface.
func ParseRole(s string) (Role, error) {
	if s == "" {
		return DefaultRole, nil
	}
	if _, ok := roleScopes[Role(s)]; !ok {
		return "", fmt.Errorf("unknown role %q (want viewer, operator, or admin)", s)
	}
	return DefaultRole, nil
}

// Scopes returns the scopes the role granted before roles were dropped.
// Authorization uses [Device.EffectiveScopes] instead.
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

// EffectiveScopes are the scopes a device is authorized with: all of
// them, whatever its stored role and scopes say.
func (d Device) EffectiveScopes() []string { return AllScopes() }

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
	if d.Rev < 1 {
		return errors.New("device record: bad rev")
	}
	for _, f := range []string{d.Name, d.Platform, d.Model, d.AppVersion, d.PairedVia, d.ApprovedBy, d.UpdatedBy} {
		if len(f) > maxTextBytes {
			return errors.New("device record: text field too long")
		}
	}
	allowed := append(d.Role.Scopes(), optInScopes...)
	for _, sc := range d.Scopes {
		if !slices.Contains(allowed, sc) {
			return fmt.Errorf("device record: scope %q not allowed for role %s", sc, d.Role)
		}
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
	Version   int      `json:"version"`
	ClusterID string   `json:"cluster_id,omitempty"`
	Devices   []Device `json:"devices"`
}

// Registry is this node's copy of the replicated device registry,
// persisted to devices.json (0600).
type Registry struct {
	path string

	mu        sync.Mutex
	clusterID string
	devices   map[string]Device

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
	r.clusterID = f.ClusterID
	for _, d := range f.Devices {
		if d.Validate() == nil {
			r.devices[d.DeviceID] = d
		}
	}
	return r, nil
}

// BindCluster ties the registry to clusterID. Devices paired in another
// cluster (this node left and joined elsewhere) are dropped, so trust never
// carries across clusters.
func (r *Registry) BindCluster(clusterID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clusterID == clusterID {
		return nil
	}
	if r.clusterID != "" && len(r.devices) > 0 {
		slog.Warn("Dropping devices paired in a previous cluster", "devices", len(r.devices))
		r.devices = map[string]Device{}
	}
	r.clusterID = clusterID
	return r.saveLocked()
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

// HasActive reports whether any device is paired and not revoked.
func (r *Registry) HasActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.devices {
		if !d.Revoked() {
			return true
		}
	}
	return false
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
	f := registryFile{Version: devicesVersion, ClusterID: r.clusterID, Devices: make([]Device, 0, len(r.devices))}
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
